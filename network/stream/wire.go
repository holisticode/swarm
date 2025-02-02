// Copyright 2019 The Swarm Authors
// This file is part of the Swarm library.
//
// The Swarm library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The Swarm library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the Swarm library. If not, see <http://www.gnu.org/licenses/>.

package stream

import (
	"context"
	"fmt"

	"github.com/holisticode/swarm/chunk"
	"github.com/holisticode/swarm/storage"
)

// StreamProvider interface provides a lightweight abstraction that allows an easily-pluggable
// stream provider as part of the Stream! protocol specification.
// Since Stream! thoroughly defines the concepts of a stream, intervals, clients and servers, the
// interface therefore needs only a pluggable provider.
// The domain interpretable notions which are at the discretion of the implementing
// provider therefore are - sourcing data (get, put, subscribe for constant new data, and need data
// which is to decide whether to retrieve data or not), retrieving cursors from the data store, the
// implementation of which streams to maintain with a certain peer and providing functionality
// to expose, parse and encode values related to the string represntation of the stream
type StreamProvider interface {

	// NeedData informs the caller whether a certain chunk needs to be fetched from another peer or not
	NeedData(ctx context.Context, addr ...chunk.Address) ([]bool, error)

	// Get a set of chunks identified by addr from the local storage
	Get(ctx context.Context, addr ...chunk.Address) ([]chunk.Chunk, error)

	// Put a set of chunks into the local storage
	Put(ctx context.Context, ch ...chunk.Chunk) (exists []bool, err error)

	// Set a set of chunks as synced in the localstore
	Set(ctx context.Context, addrs ...chunk.Address) error

	// Subscribe to a data stream from an arbitrary data source
	Subscribe(ctx context.Context, key interface{}, from, to uint64) (<-chan chunk.Descriptor, func())

	// Cursor returns the last known Cursor for a given Stream Key string
	Cursor(string) (uint64, error)

	// InitPeer is a provider specific implementation on how to maintain running streams with
	// an arbitrary Peer. This method should always be run in a separate goroutine
	InitPeer(p *Peer)

	// WantStream indicates if we are interested in a stream
	WantStream(*Peer, ID) bool

	// StreamName returns the Name of the Stream (see ID)
	StreamName() string

	// ParseStream from a standard pipe-separated string and return the Stream Key
	ParseKey(string) (interface{}, error)

	// EncodeStream from a Stream Key to a Stream pipe-separated string representation
	EncodeKey(interface{}) (string, error)

	// Autostart indicates if the stream should autostart
	Autostart() bool

	// Boundedness indicates if the stream is bounded or not
	Boundedness() bool

	// Close the provider
	Close()
}

// StreamInfoReq is a request to get information about particular streams
type StreamInfoReq struct {
	Streams []ID
}

// StreamInfoRes is a response to StreamInfoReq with the corresponding stream descriptors
type StreamInfoRes struct {
	Streams []StreamDescriptor
}

// StreamDescriptor describes an arbitrary stream
type StreamDescriptor struct {
	Stream  ID
	Cursor  uint64
	Bounded bool
}

// GetRange is a message sent from the downstream peer to the upstream peer asking for chunks
// within a particular interval for a certain stream
type GetRange struct {
	Ruid      uint
	Stream    ID
	From      uint64
	To        *uint64 `rlp:"nil"`
	BatchSize uint
}

// OfferedHashes is a message sent from the upstream peer to the downstream peer allowing the latter
// to selectively ask for chunks within a particular requested interval
type OfferedHashes struct {
	Ruid      uint
	LastIndex uint64
	Hashes    []byte
}

// WantedHashes is a message sent from the downstream peer to the upstream peer in response
// to OfferedHashes in order to selectively ask for a particular chunks within an interval
type WantedHashes struct {
	Ruid      uint
	BitVector []byte
}

// ChunkDelivery delivers a frame of chunks in response to a WantedHashes message
type ChunkDelivery struct {
	Ruid   uint
	Chunks []DeliveredChunk
}

// DeliveredChunk encapsulates a particular chunk's underlying data within a ChunkDelivery message
type DeliveredChunk struct {
	Addr storage.Address //chunk address
	Data []byte          //chunk data
}

// StreamState is a message exchanged between two nodes to notify of changes or errors in a stream's state
type StreamState struct {
	Stream  ID
	Code    uint16
	Message string
}

// Stream defines a unique stream identifier in a textual representation
type ID struct {
	// Name is used for the Stream provider identification
	Name string
	// Key is the name of specific data stream within the stream provider. The semantics of this value
	// is at the discretion of the stream provider implementation
	Key string
}

// NewID returns a new Stream ID for a particular stream Name and Key
func NewID(name string, key string) ID {
	return ID{
		Name: name,
		Key:  key,
	}
}

// String return a stream id based on all Stream fields.
func (s ID) String() string {
	return fmt.Sprintf("%s|%s", s.Name, s.Key)
}
