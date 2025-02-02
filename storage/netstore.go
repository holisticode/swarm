// Copyright 2016 The go-ethereum Authors
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
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/metrics"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/holisticode/swarm/chunk"
	"github.com/holisticode/swarm/network/timeouts"
	"github.com/holisticode/swarm/spancontext"
	lru "github.com/hashicorp/golang-lru"
	olog "github.com/opentracing/opentracing-go/log"
	"github.com/syndtr/goleveldb/leveldb"
	"golang.org/x/sync/singleflight"

	"github.com/holisticode/swarm/log"
	"github.com/holisticode/swarm/network"
)

const (
	// capacity for the fetchers LRU cache
	fetchersCapacity = 500000
)

var (
	ErrNoSuitablePeer = errors.New("no suitable peer")
)

// Fetcher is a struct which maintains state of remote requests.
// Fetchers are stored in fetchers map and signal to all interested parties if a given chunk is delivered
// the mutex controls who closes the channel, and make sure we close the channel only once
type Fetcher struct {
	Delivered chan struct{} // when closed, it means that the chunk this Fetcher refers to is delivered
	Chunk     chunk.Chunk   // the delivered chunk data

	// it is possible for multiple actors to be delivering the same chunk,
	// for example through syncing and through retrieve request. however we want the `Delivered` channel to be closed only
	// once, even if we put the same chunk multiple times in the NetStore.
	once sync.Once

	CreatedAt time.Time // timestamp when the fetcher was created, used for metrics measuring lifetime of fetchers
	CreatedBy string    // who created the fetcher - "request" or "syncing", used for metrics measuring lifecycle of fetchers

	RequestedBySyncer bool // whether we have issued at least once a request through Offered/Wanted hashes flow
}

// NewFetcher is a constructor for a Fetcher
func NewFetcher() *Fetcher {
	return &Fetcher{
		Delivered:         make(chan struct{}),
		once:              sync.Once{},
		CreatedAt:         time.Now(),
		CreatedBy:         "",
		RequestedBySyncer: false,
	}
}

// SafeClose signals to interested parties (those waiting for a signal on fi.Delivered) that a chunk is delivered.
// It sets the delivered chunk data to the fi.Chunk field, then closes the fi.Delivered channel through the
// sync.Once object, because it is possible for a chunk to be delivered multiple times concurrently.
func (fi *Fetcher) SafeClose(ch chunk.Chunk) {
	fi.once.Do(func() {
		fi.Chunk = ch
		close(fi.Delivered)
	})
}

type RemoteGetFunc func(ctx context.Context, req *Request, localID enode.ID) (*enode.ID, func(), error)

// NetStore is an extension of LocalStore
// it implements the ChunkStore interface
// on request it initiates remote cloud retrieval
type NetStore struct {
	chunk.Store
	LocalID      enode.ID // our local enode - used when issuing RetrieveRequests
	fetchers     *lru.Cache
	putMu        sync.Mutex
	requestGroup singleflight.Group
	RemoteGet    RemoteGetFunc
	logger       log.Logger
}

// NewNetStore creates a new NetStore using the provided chunk.Store and localID of the node.
func NewNetStore(store chunk.Store, baseAddr *network.BzzAddr) *NetStore {
	fetchers, _ := lru.New(fetchersCapacity)

	return &NetStore{
		fetchers: fetchers,
		Store:    store,
		LocalID:  baseAddr.ID(),
		logger:   log.NewBaseAddressLogger(baseAddr.ShortString()),
	}
}

// Put stores a chunk in localstore, and delivers to all requestor peers using the fetcher stored in
// the fetchers cache
func (n *NetStore) Put(ctx context.Context, mode chunk.ModePut, chs ...Chunk) ([]bool, error) {
	// first notify all goroutines waiting on the fetcher that the chunk has been received

	n.putMu.Lock()
	for i, ch := range chs {
		n.logger.Trace("netstore.put", "index", i, "ref", ch.Address().String(), "mode", mode)
		fi, ok := n.fetchers.Get(ch.Address().String())
		if ok {
			// we need SafeClose, because it is possible for a chunk to both be
			// delivered through syncing and through a retrieve request
			fii := fi.(*Fetcher)
			fii.SafeClose(ch)
		}
	}
	n.putMu.Unlock()

	// put the chunk to the localstore, there should be no error
	exist, err := n.Store.Put(ctx, mode, chs...)
	if err != nil {
		return nil, err
	}

	n.putMu.Lock()
	defer n.putMu.Unlock()

	for _, ch := range chs {
		fi, ok := n.fetchers.Get(ch.Address().String())
		if ok {
			fii := fi.(*Fetcher)
			n.logger.Trace("netstore.put chunk delivered and stored", "ref", ch.Address().String())

			metrics.GetOrRegisterResettingTimer(fmt.Sprintf("netstore/fetcher/lifetime/%s", fii.CreatedBy), nil).UpdateSince(fii.CreatedAt)

			// helper snippet to log if a chunk took way to long to be delivered
			if time.Since(fii.CreatedAt) > timeouts.FetcherSlowChunkDeliveryThreshold {
				metrics.GetOrRegisterCounter("netstore/slow_chunk_delivery", nil).Inc(1)
				n.logger.Trace("netstore.put slow chunk delivery", "ref", ch.Address().String())
			}
			n.fetchers.Remove(ch.Address().String())
		}
	}

	return exist, nil
}

// Close chunk store
func (n *NetStore) Close() error {
	return n.Store.Close()
}

// Get retrieves a chunk
// If it is not found in the LocalStore then it uses RemoteGet to fetch from the network.
func (n *NetStore) Get(ctx context.Context, mode chunk.ModeGet, req *Request) (ch Chunk, err error) {
	metrics.GetOrRegisterCounter("netstore/get", nil).Inc(1)
	start := time.Now()

	ref := req.Addr

	ch, err = n.Store.Get(ctx, mode, ref)
	if err != nil {
		// TODO: fix comparison - we should be comparing against leveldb.ErrNotFound, this error should be wrapped.
		if err != ErrChunkNotFound && err != leveldb.ErrNotFound {
			n.logger.Error("localstore get error", "err", err)
		}

		n.logger.Trace("netstore.chunk-not-in-localstore", "ref", ref.String())

		v, err, _ := n.requestGroup.Do(ref.String(), func() (interface{}, error) {
			// currently we issue a retrieve request if a fetcher
			// has already been created by a syncer for that particular chunk.
			// so it is possible to
			// have 2 in-flight requests for the same chunk - one by a
			// syncer (offered/wanted/deliver flow) and one from
			// here - retrieve request
			fi, _, ok := n.GetOrCreateFetcher(ctx, ref, "request")
			if ok {
				ch, err = n.RemoteFetch(ctx, req, fi)
				if err != nil {
					return nil, err
				}
			}

			// fi could be nil (when ok == false) if the chunk was added to the NetStore between n.store.Get and the call to n.GetOrCreateFetcher
			if fi != nil {
				metrics.GetOrRegisterResettingTimer(fmt.Sprintf("fetcher/%s/request", fi.CreatedBy), nil).UpdateSince(start)
			}

			return ch, nil
		})

		if err != nil {
			n.logger.Trace(err.Error(), "ref", ref)
			return nil, err
		}

		n.logger.Trace("netstore.singleflight returned", "ref", ref.String(), "err", err)

		return v.(Chunk), nil
	}
	n.logger.Trace("netstore.get returned", "ref", ref.String())

	ctx, ssp := spancontext.StartSpan(
		ctx,
		"localstore.get")
	defer ssp.Finish()

	return ch, nil
}

// RemoteFetch is handling the retry mechanism when making a chunk request to our peers.
// For a given chunk Request, we call RemoteGet, which selects the next eligible peer and
// issues a RetrieveRequest and we wait for a delivery. If a delivery doesn't arrive within the SearchTimeout
// we retry.
func (n *NetStore) RemoteFetch(ctx context.Context, req *Request, fi *Fetcher) (chunk.Chunk, error) {
	// while we haven't timed-out, and while we don't have a chunk,
	// iterate over peers and try to find a chunk
	metrics.GetOrRegisterCounter("remote/fetch", nil).Inc(1)

	ref := req.Addr

	for {
		metrics.GetOrRegisterCounter("remote/fetch/inner", nil).Inc(1)

		ctx, osp := spancontext.StartSpan(
			ctx,
			"remote.fetch")
		osp.LogFields(olog.String("ref", ref.String()))

		ctx = context.WithValue(ctx, "remote.fetch", osp)

		log.Trace("remote.fetch", "ref", ref)

		currentPeer, cleanup, err := n.RemoteGet(ctx, req, n.LocalID)
		if err != nil {
			n.logger.Trace(err.Error(), "ref", ref)
			osp.LogFields(olog.String("err", err.Error()))
			osp.Finish()
			return nil, ErrNoSuitablePeer
		}
		defer cleanup()

		// add peer to the set of peers to skip from now
		n.logger.Trace("remote.fetch, adding peer to skip", "ref", ref, "peer", currentPeer.String())
		req.PeersToSkip.Store(currentPeer.String(), time.Now())

		select {
		case <-fi.Delivered:
			n.logger.Trace("remote.fetch, chunk delivered", "ref", ref, "base", hex.EncodeToString(n.LocalID[:16]))

			osp.LogFields(olog.Bool("delivered", true))
			osp.Finish()
			return fi.Chunk, nil
		case <-time.After(timeouts.SearchTimeout):
			metrics.GetOrRegisterCounter("remote/fetch/timeout/search", nil).Inc(1)

			osp.LogFields(olog.Bool("timeout", true))
			osp.Finish()
			break
		case <-ctx.Done(): // global fetcher timeout
			n.logger.Trace("remote.fetch, global timeout fail", "ref", ref, "err", ctx.Err())
			metrics.GetOrRegisterCounter("remote/fetch/timeout/global", nil).Inc(1)

			osp.LogFields(olog.Bool("fail", true))
			osp.Finish()
			return nil, ctx.Err()
		}
	}
}

// Has is the storage layer entry point to query the underlying
// database to return if it has a chunk or not.
func (n *NetStore) Has(ctx context.Context, ref Address) (bool, error) {
	return n.Store.Has(ctx, ref)
}

// GetOrCreateFetcher returns the Fetcher for a given chunk, if this chunk is not in the LocalStore.
// If the chunk is in the LocalStore, it returns nil for the Fetcher and ok == false
func (n *NetStore) GetOrCreateFetcher(ctx context.Context, ref Address, interestedParty string) (f *Fetcher, loaded bool, ok bool) {
	n.putMu.Lock()
	defer n.putMu.Unlock()

	f = NewFetcher()
	v, loaded := n.fetchers.Get(ref.String())
	n.logger.Trace("netstore.has-with-callback.loadorstore", "localID", n.LocalID.String()[:16], "ref", ref.String(), "loaded", loaded, "createdBy", interestedParty)
	if loaded {
		f = v.(*Fetcher)
	} else {
		f.CreatedBy = interestedParty
		n.fetchers.Add(ref.String(), f)
	}

	// if fetcher created by request, but we get a call from syncer, make sure we issue a second request
	if f.CreatedBy != interestedParty && !f.RequestedBySyncer {
		f.RequestedBySyncer = true
		return f, false, true
	}

	return f, loaded, true
}
