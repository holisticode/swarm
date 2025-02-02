// Copyright 2019 The go-ethereum Authors
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

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/metrics"
	"github.com/holisticode/swarm/chunk"
	"github.com/holisticode/swarm/log"
	"github.com/holisticode/swarm/network"
	"github.com/holisticode/swarm/network/stream"
	"github.com/holisticode/swarm/storage"
	"github.com/holisticode/swarm/storage/localstore"
)

const InspectorIsPullSyncingTolerance = 15 * time.Second

type Inspector struct {
	api      *API
	hive     *network.Hive
	netStore *storage.NetStore
	stream   *stream.Registry
	ls       *localstore.DB
}

func NewInspector(api *API, hive *network.Hive, netStore *storage.NetStore, pullSyncer *stream.Registry, ls *localstore.DB) *Inspector {
	return &Inspector{api, hive, netStore, pullSyncer, ls}
}

// Hive prints the kademlia table
func (i *Inspector) Hive() string {
	return i.hive.String()
}

// KademliaInfo returns structured output of the Kademlia state that we can check for equality
func (i *Inspector) KademliaInfo() network.KademliaInfo {
	return i.hive.KademliaInfo()
}

func (i *Inspector) IsPushSynced(tagname string) bool {
	tags := i.api.Tags.All()

	for _, t := range tags {
		if t.Name == tagname {
			ds := t.Done(chunk.StateSynced)
			log.Trace("found tag", "tagname", tagname, "done-syncing", ds)
			return ds
		}
	}

	return false
}

func (i *Inspector) IsPullSyncing() bool {
	t := i.stream.LastReceivedChunkTime()

	// if last received chunks msg time is after now-15sec. (i.e. within the last 15sec.) then we say that the node is still syncing
	// technically this is not correct, because this might have been a retrieve request, but for the time being it works for our purposes
	// because we know we are not making retrieve requests on the node while checking this
	return t.After(time.Now().Add(-InspectorIsPullSyncingTolerance))
}

// DeliveriesPerPeer returns the sum of chunks we received from a given peer
func (i *Inspector) DeliveriesPerPeer() map[string]int64 {
	res := map[string]int64{}

	// iterate connection in kademlia
	i.hive.Kademlia.EachConn(nil, 255, func(p *network.Peer, po int) bool {
		// get how many chunks we receive for retrieve requests per peer
		peermetric := fmt.Sprintf("network.retrieve.chunk.delivery.%x", p.Over()[:16])

		res[fmt.Sprintf("%x", p.Over()[:16])] = metrics.GetOrRegisterCounter(peermetric, nil).Count()

		return true
	})

	return res
}

// Has checks whether each chunk address is present in the underlying datastore,
// the bool in the returned structs indicates if the underlying datastore has
// the chunk stored with the given address (true), or not (false)
func (i *Inspector) Has(chunkAddresses []storage.Address) string {
	hostChunks := []string{}
	for _, addr := range chunkAddresses {
		has, err := i.netStore.Has(context.Background(), addr)
		if err != nil {
			log.Error(err.Error())
		}
		if has {
			hostChunks = append(hostChunks, "1")
		} else {
			hostChunks = append(hostChunks, "0")
		}
	}

	return strings.Join(hostChunks, "")
}

func (i *Inspector) PeerStreams() (string, error) {
	peerInfo, err := i.stream.PeerInfo()
	if err != nil {
		return "", err
	}
	v, err := json.Marshal(peerInfo)
	if err != nil {
		return "", err
	}
	return string(v), nil
}

func (i *Inspector) StorageIndices() (map[string]int, error) {
	return i.ls.DebugIndices()
}
