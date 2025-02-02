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

package storage

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/holisticode/swarm/chunk"
	"github.com/holisticode/swarm/storage/encryption"
	"golang.org/x/crypto/sha3"
)

const (
	noOfStorageWorkers = 150 // Since we want 128 data chunks to be processed parallel + few for processing tree chunks

)

type hasherStore struct {
	// nrChunks is used with atomic functions
	// it is required to be at the start of the struct to ensure 64bit alignment for ARM, x86-32, and 32-bit MIPS architectures
	// see: https://golang.org/pkg/sync/atomic/#pkg-note-BUG
	nrChunks  uint64 // number of chunks to store
	store     ChunkStore
	tag       *chunk.Tag
	toEncrypt bool
	doWait    sync.Once
	hashFunc  SwarmHasher
	hashSize  int           // content hash size
	refSize   int64         // reference size (content hash + possibly encryption key)
	errC      chan error    // global error channel
	waitC     chan error    // global wait channel
	doneC     chan struct{} // closed by Close() call to indicate that count is the final number of chunks
	quitC     chan struct{} // closed to quit unterminated routines
	workers   chan Chunk    // back pressure for limiting storage workers goroutines
}

// NewHasherStore creates a hasherStore object, which implements Putter and Getter interfaces.
// With the HasherStore you can put and get chunk data (which is just []byte) into a ChunkStore
// and the hasherStore will take core of encryption/decryption of data if necessary
func NewHasherStore(store ChunkStore, hashFunc SwarmHasher, toEncrypt bool, tag *chunk.Tag) *hasherStore {
	hashSize := hashFunc().Size()
	refSize := int64(hashSize)
	if toEncrypt {
		refSize += encryption.KeyLength
	}

	h := &hasherStore{
		store:     store,
		tag:       tag,
		toEncrypt: toEncrypt,
		hashFunc:  hashFunc,
		hashSize:  hashSize,
		refSize:   refSize,
		errC:      make(chan error),
		waitC:     make(chan error),
		doneC:     make(chan struct{}),
		quitC:     make(chan struct{}),
		workers:   make(chan Chunk, noOfStorageWorkers),
	}
	return h
}

// Put stores the chunkData into the ChunkStore of the hasherStore and returns the reference.
// If hasherStore has a chunkEncryption object, the data will be encrypted.
// Asynchronous function, the data will not necessarily be stored when it returns.
func (h *hasherStore) Put(ctx context.Context, chunkData ChunkData) (Reference, error) {
	c := chunkData
	var encryptionKey encryption.Key
	if h.toEncrypt {
		var err error
		c, encryptionKey, err = h.encryptChunkData(chunkData)
		if err != nil {
			return nil, err
		}
	}
	chunk := h.createChunk(c)
	h.storeChunk(ctx, chunk)

	// Start the wait function which will detect completion of put
	h.doWait.Do(func() {
		go h.startWait(ctx)
	})

	return Reference(append(chunk.Address(), encryptionKey...)), nil
}

// Get returns data of the chunk with the given reference (retrieved from the ChunkStore of hasherStore).
// If the data is encrypted and the reference contains an encryption key, it will be decrypted before
// return.
func (h *hasherStore) Get(ctx context.Context, ref Reference) (ChunkData, error) {
	addr, encryptionKey, err := parseReference(ref, h.hashSize)
	if err != nil {
		return nil, err
	}

	chunk, err := h.store.Get(ctx, chunk.ModeGetRequest, addr)
	if err != nil {
		return nil, err
	}

	chunkData := ChunkData(chunk.Data())
	toDecrypt := (encryptionKey != nil)
	if toDecrypt {
		var err error
		chunkData, err = h.decryptChunkData(chunkData, encryptionKey)
		if err != nil {
			return nil, err
		}
	}
	return chunkData, nil
}

// Close indicates that no more chunks will be put with the hasherStore, so the Wait
// function can return when all the previously put chunks has been stored.
func (h *hasherStore) Close() {
	close(h.doneC)
}

// Wait returns when
//    1) the Close() function has been called and
//    2) all the chunks which has been Put has been stored
//    OR
//    1) if there is error while storing chunk
func (h *hasherStore) Wait(ctx context.Context) error {
	defer close(h.quitC)
	err := <-h.waitC
	return err
}

func (h *hasherStore) startWait(ctx context.Context) {
	var nrStoredChunks uint64 // number of stored chunks
	var done bool
	doneC := h.doneC
	for {
		select {
		// if context is done earlier, just return with the error
		case <-ctx.Done():
			select {
			case h.waitC <- ctx.Err():
			case <-h.quitC:
			}
			return
		// doneC is closed if all chunks have been submitted, from then we just wait until all of them are also stored
		case <-doneC:
			done = true
			doneC = nil
		// a chunk has been stored, if err is nil, then successfully, so increase the stored chunk counter
		case err := <-h.errC:
			if err != nil {
				select {
				case h.waitC <- err:
				case <-h.quitC:
				}
				return
			}
			nrStoredChunks++
		}
		// if all the chunks have been submitted and all of them are stored, then we can return
		if done {
			if nrStoredChunks >= atomic.LoadUint64(&h.nrChunks) {
				h.waitC <- nil
				break
			}
		}
	}
}

func (h *hasherStore) createHash(chunkData ChunkData) Address {
	hasher := h.hashFunc()
	hasher.Reset()
	hasher.SetSpanBytes(chunkData[:8]) // 8 bytes of length
	hasher.Write(chunkData[8:])        // minus 8 []byte length
	return hasher.Sum(nil)
}

func (h *hasherStore) createChunk(chunkData ChunkData) Chunk {
	hash := h.createHash(chunkData)
	chunk := NewChunk(hash, chunkData).WithTagID(h.tag.Uid)
	return chunk
}

func (h *hasherStore) encryptChunkData(chunkData ChunkData) (ChunkData, encryption.Key, error) {
	if len(chunkData) < 8 {
		return nil, nil, fmt.Errorf("Invalid ChunkData, min length 8 got %v", len(chunkData))
	}

	key, encryptedSpan, encryptedData, err := h.encrypt(chunkData)
	if err != nil {
		return nil, nil, err
	}
	c := make(ChunkData, len(encryptedSpan)+len(encryptedData))
	copy(c[:8], encryptedSpan)
	copy(c[8:], encryptedData)
	return c, key, nil
}

func (h *hasherStore) decryptChunkData(chunkData ChunkData, encryptionKey encryption.Key) (ChunkData, error) {
	if len(chunkData) < 8 {
		return nil, fmt.Errorf("Invalid ChunkData, min length 8 got %v", len(chunkData))
	}

	decryptedSpan, decryptedData, err := h.decrypt(chunkData, encryptionKey)
	if err != nil {
		return nil, err
	}

	// removing extra bytes which were just added for padding
	length := ChunkData(decryptedSpan).Size()
	for length > chunk.DefaultSize {
		length = length + (chunk.DefaultSize - 1)
		length = length / chunk.DefaultSize
		length *= uint64(h.refSize)
	}

	c := make(ChunkData, length+8)
	copy(c[:8], decryptedSpan)
	copy(c[8:], decryptedData[:length])

	return c, nil
}

func (h *hasherStore) RefSize() int64 {
	return h.refSize
}

func (h *hasherStore) encrypt(chunkData ChunkData) (encryption.Key, []byte, []byte, error) {
	key := encryption.GenerateRandomKey(encryption.KeyLength)
	encryptedSpan, err := h.newSpanEncryption(key).Encrypt(chunkData[:8])
	if err != nil {
		return nil, nil, nil, err
	}
	encryptedData, err := h.newDataEncryption(key).Encrypt(chunkData[8:])
	if err != nil {
		return nil, nil, nil, err
	}
	return key, encryptedSpan, encryptedData, nil
}

func (h *hasherStore) decrypt(chunkData ChunkData, key encryption.Key) ([]byte, []byte, error) {
	encryptedSpan, err := h.newSpanEncryption(key).Encrypt(chunkData[:8])
	if err != nil {
		return nil, nil, err
	}
	encryptedData, err := h.newDataEncryption(key).Encrypt(chunkData[8:])
	if err != nil {
		return nil, nil, err
	}
	return encryptedSpan, encryptedData, nil
}

func (h *hasherStore) newSpanEncryption(key encryption.Key) encryption.Encryption {
	return encryption.New(key, 0, uint32(chunk.DefaultSize/h.refSize), sha3.NewLegacyKeccak256)
}

func (h *hasherStore) newDataEncryption(key encryption.Key) encryption.Encryption {
	return encryption.New(key, int(chunk.DefaultSize), 0, sha3.NewLegacyKeccak256)
}

func (h *hasherStore) storeChunk(ctx context.Context, ch Chunk) {
	h.workers <- ch
	atomic.AddUint64(&h.nrChunks, 1)
	go func() {
		defer func() {
			<-h.workers
		}()
		seen, err := h.store.Put(ctx, chunk.ModePutUpload, ch)
		h.tag.Inc(chunk.StateStored)
		if err == nil && seen[0] {
			h.tag.Inc(chunk.StateSeen)
		}
		select {
		case h.errC <- err:
		case <-h.quitC:
		}
	}()
}

func parseReference(ref Reference, hashSize int) (Address, encryption.Key, error) {
	encryptedRefLength := hashSize + encryption.KeyLength
	switch len(ref) {
	case AddressLength:
		return Address(ref), nil, nil
	case encryptedRefLength:
		encKeyIdx := len(ref) - encryption.KeyLength
		return Address(ref[:encKeyIdx]), encryption.Key(ref[encKeyIdx:]), nil
	default:
		return nil, nil, fmt.Errorf("Invalid reference length, expected %v or %v got %v", hashSize, encryptedRefLength, len(ref))
	}
}
