// Copyright 2018 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

// Package bmt provides a binary merkle tree implementation used for swarm chunk hash
package bmt

import (
	"context"
	"encoding/binary"
	"fmt"
	"hash"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/holisticode/swarm/file"
	"github.com/holisticode/swarm/log"
)

/*
Binary Merkle Tree Hash is a hash function over arbitrary datachunks of limited size.
It is defined as the root hash of the binary merkle tree built over fixed size segments
of the underlying chunk using any base hash function (e.g., keccak 256 SHA3).
Chunks with data shorter than the fixed size are hashed as if they had zero padding.

BMT hash is used as the chunk hash function in swarm which in turn is the basis for the
128 branching swarm hash http://swarm-guide.readthedocs.io/en/latest/architecture.html#swarm-hash

The BMT is optimal for providing compact inclusion proofs, i.e. prove that a
segment is a substring of a chunk starting at a particular offset.
The size of the underlying segments is fixed to the size of the base hash (called the resolution
of the BMT hash), Using Keccak256 SHA3 hash is 32 bytes, the EVM word size to optimize for on-chain BMT verification
as well as the hash size optimal for inclusion proofs in the merkle tree of the swarm hash.

Two implementations are provided:

* RefHasher is optimized for code simplicity and meant as a reference implementation
  that is simple to understand
* Hasher is optimized for speed taking advantage of concurrency with minimalistic
  control structure to coordinate the concurrent routines

  BMT Hasher implements the following interfaces
	* standard golang hash.Hash - synchronous, reusable
	* SwarmHash - SumWithSpan provided
	* io.Writer - synchronous left-to-right datawriter
	* AsyncWriter - concurrent section writes and asynchronous Sum call
*/

const (
	// PoolSize is the maximum number of bmt trees used by the hashers, i.e,
	// the maximum number of concurrent BMT hashing operations performed by the same hasher
	PoolSize = 8
)

var (
	ZeroSpan = make([]byte, 8)
)

// BaseHasherFunc is a hash.Hash constructor function used for the base hash of the BMT.
// implemented by Keccak256 SHA3 sha3.NewLegacyKeccak256
type BaseHasherFunc func() hash.Hash

// Hasher a reusable hasher for fixed maximum size chunks representing a BMT
// - implements the hash.Hash interface
// - reuses a pool of trees for amortised memory allocation and resource control
// - supports order-agnostic concurrent segment writes and section (double segment) writes
//   as well as sequential read and write
// - the same hasher instance must not be called concurrently on more than one chunk
// - the same hasher instance is synchronously reuseable
// - Sum gives back the tree to the pool and guaranteed to leave
//   the tree and itself in a state reusable for hashing a new chunk
// - generates and verifies segment inclusion proofs (TODO:)
type Hasher struct {
	mtx     sync.Mutex // protects Hasher.size increments (temporary solution)
	pool    *TreePool  // BMT resource pool
	bmt     *tree      // prebuilt BMT resource for flowcontrol and proofs
	size    int        // bytes written to Hasher since last Reset()
	cursor  int        // cursor to write to on next Write() call
	errFunc func(error)
	ctx     context.Context
}

// New creates a reusable BMT Hasher that
// pulls a new tree from a resource pool for hashing each chunk
func New(p *TreePool) *Hasher {
	return &Hasher{
		pool: p,
	}
}

// TreePool provides a pool of trees used as resources by the BMT Hasher.
// A tree popped from the pool is guaranteed to have a clean state ready
// for hashing a new chunk.
type TreePool struct {
	lock         sync.Mutex
	c            chan *tree     // the channel to obtain a resource from the pool
	hasher       BaseHasherFunc // base hasher to use for the BMT levels
	SegmentSize  int            // size of leaf segments, stipulated to be = hash size
	SegmentCount int            // the number of segments on the base level of the BMT
	Capacity     int            // pool capacity, controls concurrency
	Depth        int            // depth of the bmt trees = int(log2(segmentCount))+1
	Size         int            // the total length of the data (count * size)
	count        int            // current count of (ever) allocated resources
	zerohashes   [][]byte       // lookup table for predictable padding subtrees for all levels
}

// NewTreePool creates a tree pool with hasher, segment size, segment count and capacity
// on Hasher.getTree it reuses free trees or creates a new one if capacity is not reached
func NewTreePool(hasher BaseHasherFunc, segmentCount, capacity int) *TreePool {
	// initialises the zerohashes lookup table
	depth := calculateDepthFor(segmentCount)
	segmentSize := hasher().Size()
	zerohashes := make([][]byte, depth+1)
	zeros := make([]byte, segmentSize)
	zerohashes[0] = zeros
	h := hasher()
	for i := 1; i < depth+1; i++ {
		zeros = doSum(h, nil, zeros, zeros)
		zerohashes[i] = zeros
	}
	return &TreePool{
		c:            make(chan *tree, capacity),
		hasher:       hasher,
		SegmentSize:  segmentSize,
		SegmentCount: segmentCount,
		Capacity:     capacity,
		Size:         segmentCount * segmentSize,
		Depth:        depth,
		zerohashes:   zerohashes,
	}
}

// Drain drains the pool until it has no more than n resources
func (p *TreePool) Drain(n int) {
	p.lock.Lock()
	defer p.lock.Unlock()
	for len(p.c) > n {
		<-p.c
		p.count--
	}
}

// Reserve is blocking until it returns an available tree
// it reuses free trees or creates a new one if size is not reached
// TODO: should use a context here
func (p *TreePool) reserve() *tree {
	p.lock.Lock()
	defer p.lock.Unlock()
	var t *tree
	if p.count == p.Capacity {
		return <-p.c
	}
	select {
	case t = <-p.c:
	default:
		t = newTree(p.SegmentSize, p.Depth, p.hasher)
		p.count++
	}
	return t
}

// release gives back a tree to the pool.
// this tree is guaranteed to be in reusable state
func (p *TreePool) release(t *tree) {
	p.c <- t // can never fail ...
}

// tree is a reusable control structure representing a BMT
// organised in a binary tree
// Hasher uses a TreePool to obtain a tree for each chunk hash
// the tree is 'locked' while not in the pool
type tree struct {
	leaves  []*node     // leaf nodes of the tree, other nodes accessible via parent links
	cursor  int         // index of rightmost currently open segment
	offset  int         // offset (cursor position) within currently open segment
	section []byte      // the rightmost open section (double segment)
	result  chan []byte // result channel
	span    []byte      // The span of the data subsumed under the chunk
}

// node is a reuseable segment hasher representing a node in a BMT
type node struct {
	isLeft      bool      // whether it is left side of the parent double segment
	parent      *node     // pointer to parent node in the BMT
	state       int32     // atomic increment impl concurrent boolean toggle
	left, right []byte    // this is where the two children sections are written
	hasher      hash.Hash // preconstructed hasher on nodes
}

// newNode constructs a segment hasher node in the BMT (used by newTree)
func newNode(index int, parent *node, hasher hash.Hash) *node {
	return &node{
		parent: parent,
		isLeft: index%2 == 0,
		hasher: hasher,
	}
}

// Draw draws the BMT (badly)
func (t *tree) draw(hash []byte) string {
	var left, right []string
	var anc []*node
	for i, n := range t.leaves {
		left = append(left, fmt.Sprintf("%v", hashstr(n.left)))
		if i%2 == 0 {
			anc = append(anc, n.parent)
		}
		right = append(right, fmt.Sprintf("%v", hashstr(n.right)))
	}
	anc = t.leaves
	var hashes [][]string
	for l := 0; len(anc) > 0; l++ {
		var nodes []*node
		hash := []string{""}
		for i, n := range anc {
			hash = append(hash, fmt.Sprintf("%v|%v", hashstr(n.left), hashstr(n.right)))
			if i%2 == 0 && n.parent != nil {
				nodes = append(nodes, n.parent)
			}
		}
		hash = append(hash, "")
		hashes = append(hashes, hash)
		anc = nodes
	}
	hashes = append(hashes, []string{"", fmt.Sprintf("%v", hashstr(hash)), ""})
	total := 60
	del := "                             "
	var rows []string
	for i := len(hashes) - 1; i >= 0; i-- {
		var textlen int
		hash := hashes[i]
		for _, s := range hash {
			textlen += len(s)
		}
		if total < textlen {
			total = textlen + len(hash)
		}
		delsize := (total - textlen) / (len(hash) - 1)
		if delsize > len(del) {
			delsize = len(del)
		}
		row := fmt.Sprintf("%v: %v", len(hashes)-i-1, strings.Join(hash, del[:delsize]))
		rows = append(rows, row)

	}
	rows = append(rows, strings.Join(left, "  "))
	rows = append(rows, strings.Join(right, "  "))
	return strings.Join(rows, "\n") + "\n"
}

// newTree initialises a tree by building up the nodes of a BMT
// - segment size is stipulated to be the size of the hash
func newTree(segmentSize, depth int, hashfunc func() hash.Hash) *tree {
	n := newNode(0, nil, hashfunc())
	prevlevel := []*node{n}
	// iterate over levels and creates 2^(depth-level) nodes
	// the 0 level is on double segment sections so we start at depth - 2 since
	count := 2
	for level := depth - 2; level >= 0; level-- {
		nodes := make([]*node, count)
		for i := 0; i < count; i++ {
			parent := prevlevel[i/2]
			var hasher hash.Hash
			if level == 0 {
				hasher = hashfunc()
			}
			nodes[i] = newNode(i, parent, hasher)
		}
		prevlevel = nodes
		count *= 2
	}
	// the datanode level is the nodes on the last level
	return &tree{
		leaves:  prevlevel,
		result:  make(chan []byte),
		section: make([]byte, 2*segmentSize),
	}
}

// SetWriter implements file.SectionWriter
func (h *Hasher) SetWriter(_ file.SectionWriterFunc) file.SectionWriter {
	log.Warn("Synchasher does not currently support SectionWriter chaining")
	return h
}

// SectionSize implements file.SectionWriter
func (h *Hasher) SectionSize() int {
	return h.pool.SegmentSize
}

// SetSpan implements file.SectionWriter
func (h *Hasher) SetSpan(length int) {
	span := LengthToSpan(length)
	h.getTree().span = span
}

// SetSpanBytes implements storage.SwarmHash
func (h *Hasher) SetSpanBytes(b []byte) {
	t := h.getTree()
	t.span = make([]byte, 8)
	copy(t.span, b)
}

// Branches implements file.SectionWriter
func (h *Hasher) Branches() int {
	return h.pool.SegmentCount
}

// Size implements hash.Hash and file.SectionWriter
func (h *Hasher) Size() int {
	return h.pool.SegmentSize
}

// BlockSize implements hash.Hash and file.SectionWriter
func (h *Hasher) BlockSize() int {
	return 2 * h.pool.SegmentSize
}

// Sum returns the BMT root hash of the buffer
// using Sum presupposes sequential synchronous writes (io.Writer interface)
// Implements hash.Hash in file.SectionWriter
func (h *Hasher) Sum(b []byte) (s []byte) {
	t := h.getTree()
	h.mtx.Lock()
	if h.size == 0 && t.offset == 0 {
		h.mtx.Unlock()
		h.releaseTree()
		//return h.pool.zerohashes[h.pool.Depth]
		return h.GetZeroHash()
	}
	h.mtx.Unlock()
	// write the last section with final flag set to true
	go h.WriteSection(t.cursor, t.section, true, true)
	// wait for the result
	s = <-t.result
	if t.span == nil {
		t.span = LengthToSpan(h.size)
	}
	span := t.span
	// release the tree resource back to the pool
	h.releaseTree()
	return doSum(h.pool.hasher(), b, span, s)
}

// Write calls sequentially add to the buffer to be hashed,
// with every full segment calls WriteSection in a go routine
// Implements hash.Hash and file.SectionWriter
func (h *Hasher) Write(b []byte) (int, error) {
	l := len(b)
	if l == 0 || l > h.pool.Size {
		return 0, nil
	}
	h.mtx.Lock()
	h.size += len(b)
	h.mtx.Unlock()
	t := h.getTree()
	secsize := 2 * h.pool.SegmentSize
	// calculate length of missing bit to complete current open section
	smax := secsize - t.offset
	// if at the beginning of chunk or middle of the section
	if t.offset < secsize {
		// fill up current segment from buffer
		copy(t.section[t.offset:], b)
		// if input buffer consumed and open section not complete, then
		// advance offset and return
		if smax == 0 {
			smax = secsize
		}
		if l <= smax {
			t.offset += l
			return l, nil
		}
	} else {
		// if end of a section
		if t.cursor == h.pool.SegmentCount*2 {
			return 0, nil
		}
	}
	// read full sections and the last possibly partial section from the input buffer
	for smax < l {
		// section complete; push to tree asynchronously
		go h.WriteSection(t.cursor, t.section, true, false)
		// reset section
		t.section = make([]byte, secsize)
		// copy from input buffer at smax to right half of section
		copy(t.section, b[smax:])
		// advance cursor
		t.cursor++
		// smax here represents successive offsets in the input buffer
		smax += secsize
	}
	t.offset = l - smax + secsize
	return l, nil
}

// Reset implements hash.Hash and file.SectionWriter
func (h *Hasher) Reset() {
	h.cursor = 0
	h.size = 0
	h.releaseTree()
}

// releaseTree gives back the Tree to the pool whereby it unlocks
// it resets tree, segment and index
func (h *Hasher) releaseTree() {
	t := h.bmt
	if t == nil {
		return
	}
	h.bmt = nil
	go func() {
		t.cursor = 0
		t.offset = 0
		t.span = nil
		t.section = make([]byte, h.pool.SegmentSize*2)
		select {
		case <-t.result:
		default:
		}
		h.pool.release(t)
	}()
}

// Writesection writes data to the data level in the section at index i.
// Setting final to true tells the hasher no further data will be written and prepares the data for h.Sum()
// TODO remove double as argument, push responsibility for handling data context to caller
func (h *Hasher) WriteSection(i int, section []byte, double bool, final bool) {
	h.mtx.Lock()
	h.size += len(section)
	h.mtx.Unlock()
	h.writeSection(i, section, double, final)
}

// writeSection writes the hash of i-th section into level 1 node of the BMT tree
func (h *Hasher) writeSection(i int, section []byte, double bool, final bool) {
	// select the leaf node for the section
	var n *node
	var isLeft bool
	var hasher hash.Hash
	var level int
	t := h.getTree()
	if double {
		level++
		n = t.leaves[i]
		hasher = n.hasher
		isLeft = n.isLeft
		n = n.parent
		// hash the section
		section = doSum(hasher, nil, section)
	} else {
		n = t.leaves[i/2]
		hasher = n.hasher
		isLeft = i%2 == 0
	}
	// write hash into parent node
	if final {
		// for the last segment use writeFinalNode
		h.writeFinalNode(level, n, hasher, isLeft, section)
	} else {
		h.writeNode(n, hasher, isLeft, section)
	}
}

// writeNode pushes the data to the node
// if it is the first of 2 sisters written, the routine terminates
// if it is the second, it calculates the hash and writes it
// to the parent node recursively
// since hashing the parent is synchronous the same hasher can be used
func (h *Hasher) writeNode(n *node, bh hash.Hash, isLeft bool, s []byte) {
	level := 1
	for {
		// at the root of the bmt just write the result to the result channel
		if n == nil {
			h.getTree().result <- s
			return
		}
		// otherwise assign child hash to left or right segment
		if isLeft {
			n.left = s
		} else {
			n.right = s
		}
		// the child-thread first arriving will terminate
		if n.toggle() {
			return
		}
		// the thread coming second now can be sure both left and right children are written
		// so it calculates the hash of left|right and pushes it to the parent
		s = doSum(bh, nil, n.left, n.right)
		isLeft = n.isLeft
		n = n.parent
		level++
	}
}

// writeFinalNode is following the path starting from the final datasegment to the
// BMT root via parents
// for unbalanced trees it fills in the missing right sister nodes using
// the pool's lookup table for BMT subtree root hashes for all-zero sections
// otherwise behaves like `writeNode`
func (h *Hasher) writeFinalNode(level int, n *node, bh hash.Hash, isLeft bool, s []byte) {

	for {
		// at the root of the bmt just write the result to the result channel
		if n == nil {
			if s != nil {
				h.getTree().result <- s
			}
			return
		}
		var noHash bool
		if isLeft {
			// coming from left sister branch
			// when the final section's path is going via left child node
			// we include an all-zero subtree hash for the right level and toggle the node.
			n.right = h.pool.zerohashes[level]
			if s != nil {
				n.left = s
				// if a left final node carries a hash, it must be the first (and only thread)
				// so the toggle is already in passive state no need no call
				// yet thread needs to carry on pushing hash to parent
				noHash = false
			} else {
				// if again first thread then propagate nil and calculate no hash
				noHash = n.toggle()
			}
		} else {
			// right sister branch
			if s != nil {
				// if hash was pushed from right child node, write right segment change state
				n.right = s
				// if toggle is true, we arrived first so no hashing just push nil to parent
				noHash = n.toggle()

			} else {
				// if s is nil, then thread arrived first at previous node and here there will be two,
				// so no need to do anything and keep s = nil for parent
				noHash = true
			}
		}
		// the child-thread first arriving will just continue resetting s to nil
		// the second thread now can be sure both left and right children are written
		// it calculates the hash of left|right and pushes it to the parent
		if noHash {
			s = nil
		} else {
			s = doSum(bh, nil, n.left, n.right)
		}
		// iterate to parent
		isLeft = n.isLeft
		n = n.parent
		level++
	}
}

// getTree obtains a BMT resource by reserving one from the pool and assigns it to the bmt field
func (h *Hasher) getTree() *tree {
	if h.bmt != nil {
		return h.bmt
	}
	t := h.pool.reserve()
	h.bmt = t
	return t
}

// atomic bool toggle implementing a concurrent reusable 2-state object
// atomic addint with %2 implements atomic bool toggle
// it returns true if the toggler just put it in the active/waiting state
func (n *node) toggle() bool {
	return atomic.AddInt32(&n.state, 1)%2 == 1
}

// calculates the hash of the data using hash.Hash
func doSum(h hash.Hash, b []byte, data ...[]byte) []byte {
	h.Reset()
	for _, v := range data {
		h.Write(v)
	}
	return h.Sum(b)
}

// hashstr is a pretty printer for bytes used in tree.draw
func hashstr(b []byte) string {
	end := len(b)
	if end > 4 {
		end = 4
	}
	return fmt.Sprintf("%x", b[:end])
}

// calculateDepthFor calculates the depth (number of levels) in the BMT tree
func calculateDepthFor(n int) (d int) {
	c := 2
	for ; c < n; c *= 2 {
		d++
	}
	return d + 1
}

// LengthToSpan creates a binary data span size representation
// It is required for calculating the BMT hash
func LengthToSpan(length int) []byte {
	spanBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(spanBytes, uint64(length))
	return spanBytes
}

// ASYNCHASHER ACCESSORS
// All methods below here are exported to enable access for AsyncHasher
//

// GetHasher returns a new instance of the underlying hasher
func (h *Hasher) GetHasher() hash.Hash {
	return h.pool.hasher()
}

// GetZeroHash returns the zero hash of the full depth of the Hasher instance
func (h *Hasher) GetZeroHash() []byte {
	return h.pool.zerohashes[h.pool.Depth]
}

// GetTree gets the underlying tree in use by the Hasher
func (h *Hasher) GetTree() *tree {
	return h.getTree()
}

// GetTree releases the underlying tree in use by the Hasher
func (h *Hasher) ReleaseTree() {
	h.releaseTree()
}

// GetCursor returns the current write cursor for the Hasher
func (h *Hasher) GetCursor() int {
	return h.cursor
}

// GetCursor assigns the value of the current write cursor for the Hasher
func (h *Hasher) SetCursor(c int) {
	h.cursor = c
}

// GetOffset returns the write offset within the current section of the Hasher
func (t *tree) GetOffset() int {
	return t.offset
}

// GetOffset assigns the value of the write offset within the current section of the Hasher
func (t *tree) SetOffset(offset int) {
	t.offset = offset
}

// GetSection returns the current section Hasher is operating on
func (t *tree) GetSection() []byte {
	return t.section
}

// SetSection assigns the current section Hasher is operating on
func (t *tree) SetSection(b []byte) {
	t.section = b
}

// GetResult returns the result channel of the Hasher
func (t *tree) GetResult() <-chan []byte {
	return t.result
}

// GetSpan returns the span set by SetSpan
func (t *tree) GetSpan() []byte {
	return t.span
}
