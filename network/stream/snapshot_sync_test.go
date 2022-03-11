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

package stream

import (
	"context"
	"flag"
	"fmt"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/holisticode/swarm/chunk"
	"github.com/holisticode/swarm/network"
	"github.com/holisticode/swarm/network/simulation"
	"github.com/holisticode/swarm/pot"
	"github.com/holisticode/swarm/storage"
	"github.com/holisticode/swarm/testutil"
)

var (
	nodes     = flag.Int("nodes", 0, "number of nodes")
	chunks    = flag.Int("chunks", 0, "number of chunks")
	chunkSize = 4096
	pof       = network.Pof
)

type synctestConfig struct {
	addrs         [][]byte
	hashes        []storage.Address
	idToChunksMap map[enode.ID][]int
	addrToIDMap   map[string]enode.ID
}

// Tests in this file should not request chunks from peers.
// This function will panic indicating that there is a problem if request has been made.
func dummyRequestFromPeers(_ context.Context, req *storage.Request, _ enode.ID) (*enode.ID, error) {
	panic(fmt.Sprintf("unexpected request: address %s", req.Addr.String()))
}

//This test is a syncing test for nodes.
//One node is randomly selected to be the pivot node.
//A configurable number of chunks and nodes can be
//provided to the test, the number of chunks is uploaded
//to the pivot node, and we check that nodes get the chunks
//they are expected to store based on the syncing protocol.
//Number of chunks and nodes can be provided via commandline too.
func TestSyncingViaGlobalSync(t *testing.T) {
	//if nodes/chunks have been provided via commandline,
	//run the tests with these values
	if *nodes != 0 && *chunks != 0 {
		log.Info(fmt.Sprintf("Running test with %d chunks and %d nodes...", *chunks, *nodes))
		testSyncingViaGlobalSync(t, *chunks, *nodes)
	} else {
		chunkCounts := []int{4}
		nodeCounts := []int{16} // 32 nodes flakes on travis

		//if the `longrunning` flag has been provided
		//run more test combinations
		if *testutil.Longrunning {
			chunkCounts = []int{64, 128}
			nodeCounts = []int{32, 64}
		}

		for _, chunkCount := range chunkCounts {
			for _, n := range nodeCounts {
				tName := fmt.Sprintf("snapshot sync test %d nodes %d chunks", n, chunkCount)
				t.Run(tName, func(t *testing.T) {
					testSyncingViaGlobalSync(t, chunkCount, n)
				})
			}
		}
	}
}

func testSyncingViaGlobalSync(t *testing.T, chunkCount int, nodeCount int) {
	sim := simulation.NewBzzInProc(map[string]simulation.ServiceFunc{
		"bzz-sync": newSyncSimServiceFunc(&SyncSimServiceOptions{Autostart: true}),
	}, true)
	defer sim.Close()

	conf := &synctestConfig{}
	//map of discover ID to indexes of chunks expected at that ID
	conf.idToChunksMap = make(map[enode.ID][]int)
	//map of overlay address to discover ID
	conf.addrToIDMap = make(map[string]enode.ID)
	//array where the generated chunk hashes will be stored
	conf.hashes = make([]storage.Address, 0)

	ctx, cancelSimRun := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancelSimRun()

	filename := fmt.Sprintf("testdata/snapshot_%d.json", nodeCount)
	err := sim.UploadSnapshot(ctx, filename)
	if err != nil {
		t.Fatal(err)
	}

	result := runSim(conf, ctx, sim, chunkCount)
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	log.Info("Simulation ended")
}

func runSim(conf *synctestConfig, ctx context.Context, sim *simulation.Simulation, chunkCount int) simulation.Result {
	return sim.Run(ctx, func(ctx context.Context, sim *simulation.Simulation) (err error) {
		nodeIDs := sim.UpNodeIDs()
		for _, n := range nodeIDs {
			//get the kademlia overlay address from this ID
			a := n.Bytes()
			//append it to the array of all overlay addresses
			conf.addrs = append(conf.addrs, a)
			//the proximity calculation is on overlay addr,
			//the p2p/simulations check func triggers on enode.ID,
			//so we need to know which overlay addr maps to which nodeID
			conf.addrToIDMap[string(a)] = n
		}

		//get the node at that index
		//this is the node selected for upload
		node := sim.Net.GetRandomUpNode()
		uploadStore := sim.MustNodeItem(node.ID(), bucketKeyFileStore).(chunk.Store)
		hashes, err := uploadFileToSingleNodeStore(node.ID(), chunkCount, uploadStore)
		if err != nil {
			return err
		}
		conf.hashes = append(conf.hashes, hashes...)
		mapKeysToNodes(conf)

		// File retrieval check is repeated until all uploaded files are retrieved from all nodes
		// or until the timeout is reached.
	REPEAT:
		for {
			for _, id := range nodeIDs {
				//for each expected chunk, check if it is in the local store
				localChunks := conf.idToChunksMap[id]
				for _, ch := range localChunks {
					//get the real chunk by the index in the index array
					ch := conf.hashes[ch]
					log.Trace("node has chunk", "address", ch)
					//check if the expected chunk is indeed in the localstore
					var err error
					store := sim.MustNodeItem(id, bucketKeyFileStore).(chunk.Store)
					_, err = store.Get(ctx, chunk.ModeGetLookup, ch)
					if err != nil {
						log.Debug("chunk not found", "address", ch.Hex(), "node", id)
						// Do not get crazy with logging the warn message
						time.Sleep(500 * time.Millisecond)
						continue REPEAT
					}
					log.Trace("chunk found", "address", ch.Hex(), "node", id)
				}
			}
			return nil
		}
	})
}

//map chunk keys to addresses which are responsible
func mapKeysToNodes(conf *synctestConfig) {
	nodemap := make(map[string][]int)
	//build a pot for chunk hashes
	np := pot.NewPot(nil, 0)
	indexmap := make(map[string]int)
	for i, a := range conf.addrs {
		indexmap[string(a)] = i
		np, _, _ = pot.Add(np, a, pof)
	}

	ppmap := network.NewPeerPotMap(network.NewKadParams().NeighbourhoodSize, conf.addrs)

	//for each address, run EachNeighbour on the chunk hashes pot to identify closest nodes
	log.Trace(fmt.Sprintf("Generated hash chunk(s): %v", conf.hashes))
	for i := 0; i < len(conf.hashes); i++ {
		var a []byte
		np.EachNeighbour([]byte(conf.hashes[i]), pof, func(val pot.Val, po int) bool {
			// take the first address
			a = val.([]byte)
			return false
		})

		nns := ppmap[common.Bytes2Hex(a)].NNSet
		nns = append(nns, a)

		for _, p := range nns {
			nodemap[string(p)] = append(nodemap[string(p)], i)
		}
	}
	for addr, chunks := range nodemap {
		//this selects which chunks are expected to be found with the given node
		conf.idToChunksMap[conf.addrToIDMap[addr]] = chunks
	}
	log.Debug(fmt.Sprintf("Map of expected chunks by ID: %v", conf.idToChunksMap))
}

//upload a file(chunks) to a single local node store
func uploadFileToSingleNodeStore(id enode.ID, chunkCount int, store chunk.Store) ([]storage.Address, error) {
	log.Debug(fmt.Sprintf("Uploading to node id: %s", id))
	fileStore := storage.NewFileStore(store, store, storage.NewFileStoreParams(), chunk.NewTags())
	size := chunkSize
	var rootAddrs []storage.Address
	for i := 0; i < chunkCount; i++ {
		rk, wait, err := fileStore.Store(context.TODO(), testutil.RandomReader(i, size), int64(size), false)
		if err != nil {
			return nil, err
		}
		err = wait(context.TODO())
		if err != nil {
			return nil, err
		}
		rootAddrs = append(rootAddrs, (rk))
	}

	return rootAddrs, nil
}
