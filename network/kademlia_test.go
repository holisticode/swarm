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

package network

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/holisticode/swarm/network/capability"
	"github.com/holisticode/swarm/p2p/protocols"
	"github.com/holisticode/swarm/pot"
)

func init() {
	h := log.LvlFilterHandler(log.LvlWarn, log.StreamHandler(os.Stderr, log.TerminalFormat(true)))
	log.Root().SetHandler(h)
}

func testKadPeerAddr(s string) *BzzAddr {
	a := pot.NewAddressFromString(s)
	return NewBzzAddr(a, nil)
}

func newTestKademliaParams() *KadParams {
	params := NewKadParams()
	params.MinBinSize = 2
	params.NeighbourhoodSize = 2
	return params
}

type testKademlia struct {
	*Kademlia
	t *testing.T
}

func newTestKademlia(t *testing.T, b string) *testKademlia {
	base := pot.NewAddressFromString(b)
	return &testKademlia{
		Kademlia: NewKademlia(base, newTestKademliaParams()),
		t:        t,
	}
}

func (tk *testKademlia) newTestKadPeer(s string) *Peer {
	return NewPeer(&BzzPeer{BzzAddr: testKadPeerAddr(s)}, tk.Kademlia)
}

func (tk *testKademlia) newTestKadPeerWithCapabilities(s string, cap *capability.Capability) *Peer {
	addr := testKadPeerAddr(s)
	addr.Capabilities.Add(cap)
	return NewPeer(&BzzPeer{BzzAddr: addr}, tk.Kademlia)
}

func (tk *testKademlia) On(ons ...string) {
	for _, s := range ons {
		tk.Kademlia.On(tk.newTestKadPeer(s))
	}
}

func (tk *testKademlia) Off(offs ...string) {
	for _, s := range offs {
		tk.Kademlia.Off(tk.newTestKadPeer(s))
	}
}

func (tk *testKademlia) Register(regs ...string) {
	var as []*BzzAddr
	for _, s := range regs {
		as = append(as, testKadPeerAddr(s))
	}
	err := tk.Kademlia.Register(as...)
	if err != nil {
		panic(err.Error())
	}
}

// tests the validity of neighborhood depth calculations
//
// in particular, it tests that if there are one or more consecutive
// empty bins above the farthest "nearest neighbor-peer" then
// the depth should be set at the farthest of those empty bins
//
// TODO: Make test adapt to change in NeighbourhoodSize
func TestNeighbourhoodDepth(t *testing.T) {
	baseAddressBytes := RandomBzzAddr().OAddr
	kad := NewKademlia(baseAddressBytes, NewKadParams())

	baseAddress := pot.NewAddressFromBytes(baseAddressBytes)

	// generate the peers
	var peers []*Peer
	for i := 0; i < 7; i++ {
		addr := pot.RandomAddressAt(baseAddress, i)
		peers = append(peers, newTestDiscoveryPeer(addr, kad))
	}
	var sevenPeers []*Peer
	for i := 0; i < 2; i++ {
		addr := pot.RandomAddressAt(baseAddress, 7)
		sevenPeers = append(sevenPeers, newTestDiscoveryPeer(addr, kad))
	}

	testNum := 0
	// first try with empty kademlia
	depth := kad.NeighbourhoodDepth()
	if depth != 0 {
		t.Fatalf("%d expected depth 0, was %d", testNum, depth)
	}
	testNum++

	// add one peer on 7
	kad.On(sevenPeers[0])
	depth = kad.NeighbourhoodDepth()
	if depth != 0 {
		t.Fatalf("%d expected depth 0, was %d", testNum, depth)
	}
	testNum++

	// add a second on 7
	kad.On(sevenPeers[1])
	depth = kad.NeighbourhoodDepth()
	if depth != 0 {
		t.Fatalf("%d expected depth 0, was %d", testNum, depth)
	}
	testNum++

	// add from 0 to 6
	for i, p := range peers {
		kad.On(p)
		depth = kad.NeighbourhoodDepth()
		if depth != i+1 {
			t.Fatalf("%d.%d expected depth %d, was %d", i+1, testNum, i, depth)
		}
	}
	testNum++

	kad.Off(sevenPeers[1])
	depth = kad.NeighbourhoodDepth()
	if depth != 6 {
		t.Fatalf("%d expected depth 6, was %d", testNum, depth)
	}
	testNum++

	kad.Off(peers[4])
	depth = kad.NeighbourhoodDepth()
	if depth != 4 {
		t.Fatalf("%d expected depth 4, was %d", testNum, depth)
	}
	testNum++

	kad.Off(peers[3])
	depth = kad.NeighbourhoodDepth()
	if depth != 3 {
		t.Fatalf("%d expected depth 3, was %d", testNum, depth)
	}
	testNum++
}

// TestHighMinBinSize tests that the saturation function also works
// if MinBinSize is > 2, the connection count is < k.MinBinSize
// and there are more peers available than connected
func TestHighMinBinSize(t *testing.T) {
	// a function to test for different MinBinSize values
	testKad := func(minBinSize int) {
		// create a test kademlia
		tk := newTestKademlia(t, "11111111")
		// set its MinBinSize to desired value
		tk.KadParams.MinBinSize = minBinSize

		// add a couple of peers (so we have NN and depth)
		tk.On("00000000") // bin 0
		tk.On("11100000") // bin 3
		tk.On("11110000") // bin 4

		first := "10000000" // add a first peer at bin 1
		tk.Register(first)  // register it
		// we now have one registered peer at bin 1;
		// iterate and connect one peer at each iteration;
		// should be unhealthy until at minBinSize - 1
		// we connect the unconnected but registered peer
		for i := 1; i < minBinSize; i++ {
			peer := fmt.Sprintf("1000%b", 8|i)
			tk.On(peer)
			if i == minBinSize-1 {
				tk.On(first)
				tk.checkHealth(true)
				return
			}
			tk.checkHealth(false)
		}
	}
	// test MinBinSizes of 3 to 5
	testMinBinSizes := []int{3, 4, 5}
	for _, k := range testMinBinSizes {
		testKad(k)
	}
}

// TestHealthStrict tests the simplest definition of health
// Which means whether we are connected to all neighbors we know of
func TestHealthStrict(t *testing.T) {

	// base address is all ones
	// no peers
	// unhealthy (and lonely)
	tk := newTestKademlia(t, "11111111")
	tk.checkHealth(false)

	// know one peer but not connected
	// unhealthy
	tk.Register("11100000")
	tk.checkHealth(false)

	// know one peer and connected
	// unhealthy: not saturated
	tk.On("11100000")
	tk.checkHealth(true)

	// know two peers, only one connected
	// unhealthy
	tk.Register("11111100")
	tk.checkHealth(false)

	// know two peers and connected to both
	// healthy
	tk.On("11111100")
	tk.checkHealth(true)

	// know three peers, connected to the two deepest
	// healthy
	tk.Register("00000000")
	tk.checkHealth(false)

	// know three peers, connected to all three
	// healthy
	tk.On("00000000")
	tk.checkHealth(true)

	// add fourth peer deeper than current depth
	// unhealthy
	tk.Register("11110000")
	tk.checkHealth(false)

	// connected to three deepest peers
	// healthy
	tk.On("11110000")
	tk.checkHealth(true)

	// add additional peer in same bin as deepest peer
	// unhealthy
	tk.Register("11111101")
	tk.checkHealth(false)

	// four deepest of five peers connected
	// healthy
	tk.On("11111101")
	tk.checkHealth(true)

	// add additional peer in bin 0
	// unhealthy: unsaturated bin 0, 2 known but 1 connected
	tk.Register("00000001")
	tk.checkHealth(false)

	// Connect second in bin 0
	// healthy
	tk.On("00000001")
	tk.checkHealth(true)

	// add peer in bin 1
	// unhealthy, as it is known but not connected
	tk.Register("10000000")
	tk.checkHealth(false)

	// connect  peer in bin 1
	// depth change, is now 1
	// healthy, 1 peer in bin 1 known and connected
	tk.On("10000000")
	tk.checkHealth(true)

	// add second peer in bin 1
	// unhealthy, as it is known but not connected
	tk.Register("10000001")
	tk.checkHealth(false)

	// connect second peer in bin 1
	// healthy,
	tk.On("10000001")
	tk.checkHealth(true)

	// connect third peer in bin 1
	// healthy,
	tk.On("10000011")
	tk.checkHealth(true)

	// add peer in bin 2
	// unhealthy, no depth change
	tk.Register("11000000")
	tk.checkHealth(false)

	// connect peer in bin 2
	// depth change - as we already have peers in bin 3 and 4,
	// we have contiguous bins, no bin < po 5 is empty -> depth 5
	// healthy, every bin < depth has the max available peers,
	// even if they are < MinBinSize
	tk.On("11000000")
	tk.checkHealth(true)

	// add peer in bin 2
	// unhealthy, peer bin is below depth 5 but
	// has more available peers (2) than connected ones (1)
	// --> unsaturated
	tk.Register("11000011")
	tk.checkHealth(false)
}

func (tk *testKademlia) checkHealth(expectHealthy bool) {
	tk.t.Helper()
	kid := common.Bytes2Hex(tk.BaseAddr())
	addrs := [][]byte{tk.BaseAddr()}
	tk.EachAddr(nil, 255, func(addr *BzzAddr, po int) bool {
		addrs = append(addrs, addr.Address())
		return true
	})

	pp := NewPeerPotMap(tk.NeighbourhoodSize, addrs)
	healthParams := tk.GetHealthInfo(pp[kid])

	// definition of health, all conditions but be true:
	// - we at least know one peer
	// - we know all neighbors
	// - we are connected to all known neighbors
	health := healthParams.Healthy()
	if expectHealthy != health {
		tk.t.Fatalf("expected kademlia health %v, is %v\n%v", expectHealthy, health, tk.String())
	}
}

func (tk *testKademlia) checkSuggestPeer(expAddr string, expDepth int, expChanged bool) {
	tk.t.Helper()
	addr, depth, changed := tk.SuggestPeer()
	log.Trace("suggestPeer return", "addr", addr, "depth", depth, "changed", changed)
	if binStr(addr) != expAddr {
		tk.t.Fatalf("incorrect peer address suggested. expected %v, got %v", expAddr, binStr(addr))
	}
	if depth != expDepth {
		tk.t.Fatalf("incorrect saturation depth suggested. expected %v, got %v", expDepth, depth)
	}
	if changed != expChanged {
		tk.t.Fatalf("expected depth change = %v, got %v", expChanged, changed)
	}
}

func binStr(a *BzzAddr) string {
	if a == nil {
		return "<nil>"
	}
	return pot.ToBin(a.Address())[:8]
}

//Tests peer suggestion. Depends on expectedMinBinSize implementation
func TestSuggestPeers(t *testing.T) {
	base := "00000000"
	tk := newTestKademlia(t, base)

	//Add peers to bin 2 and 3 in order to be able to have depth 2
	tk.On("00100000")
	tk.On("00010000")

	//No unconnected peers
	tk.checkSuggestPeer("<nil>", 0, false)

	//We add some addresses that fall in bin0 and bin1
	tk.Register("11111000")
	tk.Register("01110000")

	//Bins should fill from  most empty to least empty and shallower to deeper
	//first suggestion should be for bin 0
	tk.checkSuggestPeer("11111000", 0, false)
	tk.On("11111000")

	//Since we now have 1 peer in bin0 and none in bin1, next suggested peer should be for bin1
	tk.checkSuggestPeer("01110000", 0, false)
	tk.On("01110000")

	tk.Register("11110000")
	tk.Register("01100000")

	//Both bins 0 and 1 have at least 1 peer, so next suggested peer should be for 0 (shallower)
	tk.checkSuggestPeer("11110000", 0, false)
	tk.On("11110000")

	//Bin0 has 2 peers, bin1 has 1 peer, should recommend peer for bin 1
	tk.checkSuggestPeer("01100000", 0, false)
	tk.On("01100000")

	tk.Register("11100000")
	tk.Register("01100011")

	//Bin1 should be saturated now
	//Next suggestion should  be bin0 peers
	tk.checkSuggestPeer("11100000", 0, false)
	tk.On("11100000")

	//Bin0 should also  be saturated now
	//All bins saturated, shouldn't suggest more peers
	tk.Register("11000000")
	tk.checkSuggestPeer("<nil>", 0, false)

	//Depth is 2
	//Since depth is 2, bins >= 2 aren't considered saturated if peers left to connect
	//We add addresses that fall in bin2 and bin3
	tk.Register("00111000")
	tk.Register("00011100")

	tk.checkSuggestPeer("00111000", 0, false)
	tk.On("00110000")
	tk.checkSuggestPeer("00011100", 0, false)
	tk.On("00011100")

	//Now depth has changed to 3 since bin3 and deeper include neighbourSize peers (2)
	//Bin0 and Bin1 not saturated, Bin2 saturated
	tk.Register("11000000")

	tk.checkSuggestPeer("01100011", 0, false)
	tk.On("01100011")
	tk.checkSuggestPeer("11000000", 0, false)
	tk.On("11000000")

	//All bins saturated again
	tk.Register("11111110")
	tk.Register("01010100")
	tk.checkSuggestPeer("<nil>", 0, false)

	//If bin in neighbour (bin3), should keep adding peers even if size >== expectedSize
	tk.Register("00011111")
	tk.checkSuggestPeer("00011111", 0, false)
	tk.On("00011111")
	tk.Register("00010001")
	tk.checkSuggestPeer("00010001", 0, false)
	tk.On("00010001")

	//No more peers left in unsaturated bins
	tk.checkSuggestPeer("<nil>", 0, false)
}

//Tests change of saturationDepth returned by suggestPeers
func TestSuggestPeersSaturationDepthChange(t *testing.T) {

	base := "00000000"
	tk := newTestKademlia(t, base)
	tk.On("10000000", "11000000", "11100000", "01000000", "01100000", "00100000", "00010000")

	//Saturation depth is 2
	if tk.saturationDepth != 2 {
		t.Fatalf("Saturation depth should be 2, got %d", tk.saturationDepth)
	}
	tk.Off("01000000")
	//Saturation depth should have fallen to 1
	tk.checkSuggestPeer("01000000", 1, true)
	tk.On("01000000")

	//Saturation depth is 2 again
	if tk.saturationDepth != 2 {
		t.Fatalf("Saturation depth should be 2, got %d", tk.saturationDepth)
	}
	tk.Off("10000000")
	//Saturation depth should have fallen to 0
	tk.checkSuggestPeer("10000000", 0, true)
	tk.On("10000000")

	tk.On("10101010", "01101010", "00101010", "00010001")
	//Saturation depth is now 3
	if tk.saturationDepth != 3 {
		t.Fatalf("Saturation depth should be 3, got %d", tk.saturationDepth)
	}
	//We remove all connections from closest bin (PO=3)
	tk.Off("00010000")
	tk.Off("00010001")
	//Saturation depth should have fallen to 2
	tk.checkSuggestPeer("00010001", 2, true)

	//We bring saturation depth back to 3
	tk.On("00010000")
	tk.On("00010001")
	if tk.saturationDepth != 3 {
		t.Fatalf("Saturation depth should be 3, got %d", tk.saturationDepth)
	}

	//We add more connections to bin 3 (closest bin) so that BinSize > expectedMinBinSize
	tk.On("00010011")
	tk.On("00011011")
	//Saturation depth shouldn't have changed
	if tk.saturationDepth != 3 {
		t.Fatalf("Saturation depth should be 3, got %d", tk.saturationDepth)
	}

	//We disconnect one peer from bin 3
	tk.Off("00010011")
	//Saturation depth shouldn't have changed
	tk.checkSuggestPeer("00010011", 0, false)

}

// a node should stay in the address book if it's removed from the kademlia
func TestOffEffectingAddressBookNormalNode(t *testing.T) {
	tk := newTestKademlia(t, "00000000")
	// peer added to kademlia
	tk.On("01000000")
	// peer should be in the address book
	if tk.defaultIndex.addrs.Size() != 1 {
		t.Fatal("known peer addresses should contain 1 entry")
	}
	// peer should be among live connections
	if tk.defaultIndex.conns.Size() != 1 {
		t.Fatal("live peers should contain 1 entry")
	}
	// remove peer from kademlia
	tk.Off("01000000")
	// peer should be in the address book
	if tk.defaultIndex.addrs.Size() != 1 {
		t.Fatal("known peer addresses should contain 1 entry")
	}
	// peer should not be among live connections
	if tk.defaultIndex.conns.Size() != 0 {
		t.Fatal("live peers should contain 0 entry")
	}
}

func TestSuggestPeerRetries(t *testing.T) {
	tk := newTestKademlia(t, "00000000")
	tk.RetryInterval = int64(300 * time.Millisecond) // cycle
	tk.MaxRetries = 50
	tk.RetryExponent = 2
	sleep := func(n int) {
		ts := tk.RetryInterval
		for i := 1; i < n; i++ {
			ts *= int64(tk.RetryExponent)
		}
		time.Sleep(time.Duration(ts))
	}

	tk.Register("01000000")
	tk.On("00000001", "00000010")
	tk.checkSuggestPeer("01000000", 0, false)

	tk.checkSuggestPeer("<nil>", 0, false)

	sleep(1)
	tk.checkSuggestPeer("01000000", 0, false)

	tk.checkSuggestPeer("<nil>", 0, false)

	sleep(1)
	tk.checkSuggestPeer("01000000", 0, false)

	tk.checkSuggestPeer("<nil>", 0, false)

	sleep(2)
	tk.checkSuggestPeer("01000000", 0, false)

	tk.checkSuggestPeer("<nil>", 0, false)

	sleep(2)
	tk.checkSuggestPeer("<nil>", 0, false)
}

func TestKademliaHiveString(t *testing.T) {
	tk := newTestKademlia(t, "00000000")
	tk.On("01000000", "00100000")
	tk.Register("10000000", "10000001")
	tk.MaxProxDisplay = 8
	h := tk.String()
	expH := "\n=========================================================================\nMon Feb 27 12:10:28 UTC 2017 KΛÐΞMLIΛ hive: queen's address: 0000000000000000000000000000000000000000000000000000000000000000\npopulation: 2 (4), NeighbourhoodSize: 2, MinBinSize: 2, MaxBinSize: 16\n============ DEPTH: 0 ==========================================\n000  0                              |  2 8100 (0) 8000 (0)\n001  1 4000                         |  1 4000 (0)\n002  1 2000                         |  1 2000 (0)\n003  0                              |  0\n004  0                              |  0\n005  0                              |  0\n006  0                              |  0\n007  0                              |  0\n========================================================================="
	if expH[104:] != h[104:] {
		t.Fatalf("incorrect hive output. expected %v, got %v", expH, h)
	}
}

func newTestDiscoveryPeer(addr pot.Address, kad *Kademlia) *Peer {
	rw := &p2p.MsgPipeRW{}
	p := p2p.NewPeer(enode.ID{}, "foo", []p2p.Cap{})
	pp := protocols.NewPeer(p, rw, &protocols.Spec{})
	bp := &BzzPeer{
		Peer:    pp,
		BzzAddr: NewBzzAddr(addr.Bytes(), []byte(fmt.Sprintf("%x", addr[:]))),
	}
	return NewPeer(bp, kad)
}

// TestKademlia_SubscribeToNeighbourhoodDepthChange checks if correct
// signaling over SubscribeToNeighbourhoodDepthChange channels are made
// when neighbourhood depth is changed.
func TestKademlia_SubscribeToNeighbourhoodDepthChange(t *testing.T) {

	testSignal := func(t *testing.T, k *testKademlia, prevDepth int, c <-chan struct{}) (newDepth int) {
		t.Helper()

		select {
		case _, ok := <-c:
			if !ok {
				t.Error("closed signal channel")
			}
			newDepth = k.NeighbourhoodDepth()
			if prevDepth == newDepth {
				t.Error("depth not changed")
			}
			return newDepth
		case <-time.After(2 * time.Second):
			t.Error("timeout")
		}
		return newDepth
	}

	t.Run("single subscription", func(t *testing.T) {
		k := newTestKademlia(t, "00000000")

		c, u := k.SubscribeToNeighbourhoodDepthChange()
		defer u()

		depth := k.NeighbourhoodDepth()

		k.On("11111101", "01000000", "10000000", "00000010")

		testSignal(t, k, depth, c)
	})

	t.Run("multiple subscriptions", func(t *testing.T) {
		k := newTestKademlia(t, "00000000")

		c1, u1 := k.SubscribeToNeighbourhoodDepthChange()
		defer u1()

		c2, u2 := k.SubscribeToNeighbourhoodDepthChange()
		defer u2()

		depth := k.NeighbourhoodDepth()

		k.On("11111101", "01000000", "10000000", "00000010")

		testSignal(t, k, depth, c1)

		testSignal(t, k, depth, c2)
	})

	t.Run("multiple changes", func(t *testing.T) {
		k := newTestKademlia(t, "00000000")

		c, u := k.SubscribeToNeighbourhoodDepthChange()
		defer u()

		depth := k.NeighbourhoodDepth()

		k.On("11111101", "01000000", "10000000", "00000010")

		depth = testSignal(t, k, depth, c)

		k.On("11111101", "01000010", "10000010", "00000110")

		testSignal(t, k, depth, c)
	})

	t.Run("no depth change", func(t *testing.T) {
		k := newTestKademlia(t, "00000000")

		c, u := k.SubscribeToNeighbourhoodDepthChange()
		defer u()

		// does not trigger the depth change
		k.On("11111101")

		select {
		case _, ok := <-c:
			if !ok {
				t.Error("closed signal channel")
			}
			t.Error("signal received")
		case <-time.After(1 * time.Second):
			// all fine
		}
	})

	t.Run("no new peers", func(t *testing.T) {
		k := newTestKademlia(t, "00000000")

		changeC, unsubscribe := k.SubscribeToNeighbourhoodDepthChange()
		defer unsubscribe()

		select {
		case _, ok := <-changeC:
			if !ok {
				t.Error("closed signal channel")
			}
			t.Error("signal received")
		case <-time.After(1 * time.Second):
			// all fine
		}
	})
}

// TestCapabilitiesIndex checks that capability indices contains only the peers that have the filters' capability bits set
// It tests the state of the indices after registering, connecting, disconnecting and removing peers
//
// It sets up peers with capability arrays 42:101, 42:001 and 666:101, and registers these capabilities as filters in the kademlia
// It also sets up a peer with both capability arrays 42:101 and 666:101
// Lastly it registers a filter for the capability 42:010 in the kademlia which will match no peers
//
// The tests are split up to make them easier to read
func TestCapabilityIndex(t *testing.T) {
	t.Run("register", testCapabilityIndexRegister)
	t.Run("connect", testCapabilityIndexConnect)
	t.Run("disconnect", testCapabilityIndexDisconnect)
	t.Run("remove", testCapabilityIndexRemove)
}

// set up capabilities and peers for each individual test
func testCapabilityIndexHelper() (*Kademlia, map[string]*Peer, map[string]*capability.Capability) {

	bzzAddrs := make(map[string]*BzzAddr)
	discPeers := make(map[string]*Peer)
	caps := make(map[string]*capability.Capability)

	kp := NewKadParams()
	addr := RandomBzzAddr()
	base := addr.OAddr
	k := NewKademlia(base, kp)

	caps["42:101"] = capability.NewCapability(42, 3)
	caps["42:101"].Set(0)
	caps["42:101"].Set(2)
	k.RegisterCapabilityIndex("42:101", *caps["42:101"])

	caps["42:001"] = capability.NewCapability(42, 3)
	caps["42:001"].Set(2)
	k.RegisterCapabilityIndex("42:001", *caps["42:001"])

	caps["42:010"] = capability.NewCapability(42, 3)
	caps["42:010"].Set(1)
	k.RegisterCapabilityIndex("42:010", *caps["42:010"])

	caps["666:101"] = capability.NewCapability(666, 3)
	caps["666:101"].Set(0)
	caps["666:101"].Set(2)
	k.RegisterCapabilityIndex("666:101", *caps["666:101"])

	bzzAddrs["42:101"] = RandomBzzAddr()
	bzzAddrs["42:101"].Capabilities.Add(caps["42:101"])
	discPeers["42:101"] = NewPeer(&BzzPeer{BzzAddr: bzzAddrs["42:101"]}, k)

	bzzAddrs["42:001"] = RandomBzzAddr()
	bzzAddrs["42:001"].Capabilities.Add(caps["42:001"])
	discPeers["42:001"] = NewPeer(&BzzPeer{BzzAddr: bzzAddrs["42:001"]}, k)

	bzzAddrs["666:101"] = RandomBzzAddr()
	bzzAddrs["666:101"].Capabilities.Add(caps["666:101"])
	discPeers["666:101"] = NewPeer(&BzzPeer{BzzAddr: bzzAddrs["666:101"]}, k)

	bzzAddrs["42:101,666:101"] = RandomBzzAddr()
	bzzAddrs["42:101,666:101"].Capabilities.Add(caps["666:101"])
	bzzAddrs["42:101,666:101"].Capabilities.Add(caps["42:101"])
	discPeers["42:101,666:101"] = NewPeer(&BzzPeer{BzzAddr: bzzAddrs["42:101,666:101"]}, k)

	k.Register(bzzAddrs["42:101"], bzzAddrs["42:001"], bzzAddrs["666:101"], bzzAddrs["42:101,666:101"])

	return k, discPeers, caps
}

// test indices after registering peers
func testCapabilityIndexRegister(t *testing.T) {

	k, _, caps := testCapabilityIndexHelper()

	// Call without filter should still return all peers
	c := 0
	k.EachAddr(k.BaseAddr(), 255, func(_ *BzzAddr, _ int) bool {
		c++
		return true
	})
	if c != 4 {
		t.Fatalf("EachAddr expected 4 peers, got %d", c)
	}

	// match capability 42:101
	c = 0
	k.EachAddrFiltered(k.BaseAddr(), "42:101", 255, func(a *BzzAddr, _ int) bool {
		c++
		cp := a.Capabilities.Get(42)
		if !cp.Match(caps["42:101"]) {
			t.Fatalf("EachAddrFiltered '42:101' capability mismatch, expected %v, got %v", caps["42:101"], cp)
		}
		return true
	})
	if c != 2 {
		t.Fatalf("EachAddrFiltered 'full' expected 2 peer, got %d", c)
	}

	// Match capability 42:001
	c = 0
	k.EachAddrFiltered(k.BaseAddr(), "42:001", 255, func(a *BzzAddr, _ int) bool {
		c++
		return true
	})
	if c != 1 {
		t.Fatalf("EachAddrFiltered '42:001' expected 1 peers, got %d", c)
	}

	// Match no capability
	c = 0
	k.EachAddrFiltered(k.BaseAddr(), "42:010", 255, func(a *BzzAddr, _ int) bool {
		c++
		return true
	})
	if c != 0 {
		t.Fatalf("EachAddrFiltered '42:010' expected 0 peers, got %d", c)
	}

	// Match 666:101
	// Also checks that one node has both 42:101 and 666:101
	c = 0
	k.EachAddrFiltered(k.BaseAddr(), "666:101", 255, func(a *BzzAddr, _ int) bool {
		c++
		cp := a.Capabilities.Get(666)
		if !cp.Match(caps["666:101"]) {
			t.Fatalf("EachAddrFiltered 'other' capability mismatch, expected %v, got %v", caps["666:101"], cp)
		}
		cp = a.Capabilities.Get(42)
		if cp != nil {
			c++
		}
		return true
	})
	if c != 3 {
		t.Fatalf("EachAddrFiltered 'other' expected 3 capability matches, got %d", c)
	}
}

// test indices after connecting peers
func testCapabilityIndexConnect(t *testing.T) {

	k, discPeers, caps := testCapabilityIndexHelper()

	// Set 42:101 and 42:101,666:101 as connected
	k.On(discPeers["42:001"])
	k.On(discPeers["42:101,666:101"])

	// Call without filter should return the single connected peer
	c := 0
	k.EachConn(k.BaseAddr(), 255, func(_ *Peer, _ int) bool {
		c++
		return true
	})
	if c != 2 {
		t.Fatalf("EachConn expected 2 peers, got %d", c)
	}

	// Check that the "42:101,666:101" peer exists in the indices for both capability arrays
	// first the "666:101" index ...
	c = 0
	k.EachConnFiltered(k.BaseAddr(), "666:101", 255, func(p *Peer, _ int) bool {
		c++
		cp := p.Capabilities.Get(666)
		if !cp.Match(caps["666:101"]) {
			t.Fatalf("EachConnFiltered '666:101' missing capability %v", caps["666:101"])
		}
		cp = p.Capabilities.Get(42)
		if !cp.Match(caps["42:101"]) {
			t.Fatalf("EachConnFiltered '666:101' missing capability %v", caps["42:101"])
		}
		return true
	})
	if c != 1 {
		t.Fatalf("EachConnFiltered 'other' expected 1 peer, got %d", c)
	}

	// ... and in 42:101
	c = 0
	k.EachConnFiltered(k.BaseAddr(), "42:101", 255, func(p *Peer, _ int) bool {
		c++
		cp := p.Capabilities.Get(666)
		if !cp.Match(caps["666:101"]) {
			t.Fatalf("EachConnFiltered '42:101' missing capability %v", caps["666:101"])
		}
		cp = p.Capabilities.Get(42)
		if !cp.Match(caps["42:101"]) {
			t.Fatalf("EachConnFiltered '42:101' missing capability %v", caps["42:101"])
		}
		return true
	})
	if c != 1 {
		t.Fatalf("EachConnFiltered 'more' expected 1 peer, got %d", c)
	}

	// check that "42:101" does not show up in "42:001"
	// since "42:001" is a subset of "42:101" (101 implements 001)
	// this is a regression test where full nodes be registered as
	// light nodes
	c = 0
	k.EachConnFiltered(k.BaseAddr(), "42:001", 255, func(p *Peer, _ int) bool {
		c++
		cp := p.Capabilities.Get(42)
		if cp.Match(caps["42:101"]) {
			t.Error("EachConnFiltered got '42:101' on '42:001' index")
		}
		return true
	})

	if c != 1 {
		t.Errorf("EachConnFiltered 'more' expected 1 peer, got %d", c)
	}

}

// test indices after disconnecting peers
func testCapabilityIndexDisconnect(t *testing.T) {

	k, discPeers, caps := testCapabilityIndexHelper()

	// Set "42:101" and "42:101,666:101" as connected
	// And then disconnect the "42:101,666:101" peer
	k.On(discPeers["42:001"])
	k.On(discPeers["42:101,666:101"])
	k.Off(discPeers["42:101,666:101"])

	// Check that the "42:101,666:101" is now removed from connections
	c := 0
	k.EachConnFiltered(k.BaseAddr(), "666:101", 255, func(_ *Peer, _ int) bool {
		c++
		return true
	})
	if c != 0 {
		t.Fatalf("EachConnFiltered '666:101' expected 0 peers, got %d", c)
	}

	// Check that there is still a "666:101" peer among known peers
	// (the two matched peers will be "42:101,666:101" and "666:101")
	c = 0
	k.EachAddrFiltered(k.BaseAddr(), "666:101", 255, func(_ *BzzAddr, _ int) bool {
		c++
		return true
	})
	if c != 2 {
		t.Fatalf("EachAddrFiltered '666:101' expected 2 peers, got %d", c)
	}

	// Check that the "42:001" peer is still registered as connected
	c = 0
	k.EachConnFiltered(k.BaseAddr(), "42:001", 255, func(p *Peer, _ int) bool {
		c++
		cp := p.Capabilities.Get(42)
		if !cp.Match(caps["42:001"]) {
			t.Fatalf("EachConnFiltered '42:001' missing capability %v", caps["42:001"])
		}
		return true
	})
	if c != 1 {
		t.Fatalf("EachConnFiltered '42:001' expected 1 peer, got %d", c)
	}
}

// test indices after (disconnecting and) removing peers
func testCapabilityIndexRemove(t *testing.T) {

	k, discPeers, caps := testCapabilityIndexHelper()

	// Set "42:101" and "42:101,666:101" as connected
	// And then disconnect the "42:101,666:101" peer
	k.On(discPeers["42:001"])
	k.On(discPeers["42:101,666:101"])
	k.Off(discPeers["42:101,666:101"])

	// Remove "less" from both connection and known peers (pruning) list
	// TODO replace with the "prune" method when one is implemented
	k.removeFromCapabilityIndex(discPeers["42:001"], false)

	// Check that the "42:001" peer is no longer registered as connected
	c := 0
	k.EachConnFiltered(k.BaseAddr(), "42:001", 255, func(p *Peer, _ int) bool {
		c++
		return true
	})
	if c != 0 {
		t.Fatalf("EachConnFiltered '42:001' expected 0 peers, got %d", c)
	}

	// check that the "42:001" peer is not known anymore
	// (the two matched peers will be "42:101,666:101" and "42:101")
	c = 0
	k.EachAddrFiltered(k.BaseAddr(), "42:001", 255, func(p *BzzAddr, _ int) bool {
		c++
		return true
	})
	if c > 0 {
		t.Fatalf("EachAddrFiltered '42:001' expected 0 peer, got %d", c)
	}

	// Remove "42:101,666:101" from known peers list (pruning only)
	// TODO replace with the "prune" method when one is implemented
	k.removeFromCapabilityIndex(discPeers["42:101,666:101"], false)

	// check that the "42:101,666:101" peer is not known anymore
	// (the only matched peer should now be "42:101")
	c = 0
	k.EachAddrFiltered(k.BaseAddr(), "42:101", 255, func(p *BzzAddr, _ int) bool {
		c++
		cp := p.Capabilities.Get(666)
		if cp != nil {
			t.Fatalf("EachAddrFiltered '42:101' should not contain a peer with capability %v", caps["666:101"])
		}
		return true
	})
	if c != 1 {
		t.Fatalf("EachAddrFiltered '42:101' expected 1 peer, got %d", c)
	}
}

// TestCapabilityNeighbourhoodDepth tests that depth calculations filtered by capability is correct
func TestCapabilityNeighbourhoodDepth(t *testing.T) {
	baseAddressBytes := RandomBzzAddr().OAddr
	kad := NewKademlia(baseAddressBytes, NewKadParams())
	cap_both := capability.NewCapability(42, 2)
	cap_both.Set(0)
	cap_both.Set(1)
	kad.RegisterCapabilityIndex("both", *cap_both)
	cap_one := capability.NewCapability(42, 2)
	cap_one.Set(0)
	kad.RegisterCapabilityIndex("one", *cap_one)

	baseAddress := pot.NewAddressFromBytes(baseAddressBytes)

	// generate the peers
	var peers []*Peer
	for i := 0; i < 2; i++ {
		addr := pot.RandomAddressAt(baseAddress, i)
		p := newTestDiscoveryPeer(addr, kad)
		p.BzzAddr.Capabilities.Add(cap_both)
		peers = append(peers)
		kad.Register(p.BzzAddr)
		kad.On(p)
	}

	addrClosestBoth := pot.RandomAddressAt(baseAddress, 7)
	peerClosestBoth := newTestDiscoveryPeer(addrClosestBoth, kad)
	peerClosestBoth.BzzAddr.Capabilities.Add(cap_both)
	kad.Register(peerClosestBoth.BzzAddr)
	kad.On(peerClosestBoth)

	addrClosestOne := pot.RandomAddressAt(baseAddress, 7)
	peerClosestOne := newTestDiscoveryPeer(addrClosestOne, kad)
	peerClosestOne.BzzAddr.Capabilities.Add(cap_one)
	kad.Register(peerClosestOne.BzzAddr)
	kad.On(peerClosestOne)

	depth, err := kad.NeighbourhoodDepthCapability("both")
	if err != nil {
		t.Fatal(err)
	}
	if depth != 1 {
		t.Fatalf("cap 'both' expected depth 1, was %d", depth)
	}

	depth, err = kad.NeighbourhoodDepthCapability("one")
	if err != nil {
		t.Fatal(err)
	}
	if depth != 0 {
		t.Fatalf("cap 'one' expected depth 0, was %d", depth)
	}
}

//TestSuggestPeerInBinByGap will check that when several addresses are available for register in the same bin, the
//one suggested is the one that fills the biggest gap of address in that bin.
func TestSuggestPeerInBinByGap(t *testing.T) {
	tk := newTestKademlia(t, "11111111")
	tk.Register("00000000", "00000001")
	bin0 := tk.getAddressBin(0)
	if bin0 == nil {
		t.Errorf("Expected bin 0 in addresses to be found but is nil")
	}

	// Adding 00000000 for example, doesn't really mater among the first two
	tk.On("00000000")
	tk.Register("01000000")
	suggestedByGapPeer := tk.suggestPeerInBinByGap(tk.getAddressBin(0))
	binaryString := bzzAddrToBinary(suggestedByGapPeer)
	// Expected suggestion is 01000000 because it covers bigger part of the address space in bin 0.
	if binaryString != "01000000" {
		t.Errorf("Expected suggestion by gap to be 01000000 because is in po=1 gap, but got %v", binaryString)
	}
	// Adding 01000000
	tk.On(binaryString)
	//Now wi will try to fill in po 1
	tk.Register("10000000", "11110000")
	bin1 := tk.getAddressBin(1)
	//Among the two peers in first one (10000000) covers more gap than the other one in our kademlia table (is farther from
	// our base 11111111)
	suggestedByGapPeer = tk.suggestPeerInBinByGap(bin1)
	binaryString = bzzAddrToBinary(suggestedByGapPeer)
	if binaryString != "10000000" {
		t.Errorf("Expected suggestion by gap to be 10000000 because is in po=1 gap, but got %v", binaryString)
	}
}

//TestSuggestPeerInBinByGapCandidate checks than when suggesting addresses, if an address in the desired gap can't be
//found, the furthest away from the reference peer will be chosen (the one with lower po so it will fill up a bigger
//part of the gap)
func TestSuggestPeerInBinByGapCandidate(t *testing.T) {
	tk := newTestKademlia(t, "11111111")
	tk.On("00000000", "10000000")
	//Registering address (10000100) po=5 from 1000000 to leave a big gap [2..4]
	tk.On("10000100")
	//Now we are going to suggest a biggest gap that doesn't match with any of the available addresses. The algorithm
	//should take the furthest from the reference address (parent of the gap, so 10000000)
	//Now we have a gap po=2 under 10000000 in bin1. We are not going to register an address po=2 (f.ex. 10100000) but
	//two addresses at po=3 and po=4 from it. Algorithm should return the farthest candidate(po=3).
	//10010000 => po=3 from 10000000
	//10001000 => po=4 from 10000000
	tk.Register("10010000", "10001000")
	suggestedCandidate := tk.suggestPeerInBinByGap(tk.getAddressBin(1))
	binaryString := bzzAddrToBinary(suggestedCandidate)
	if binaryString != "10010000" {
		t.Errorf("Expected furthest candidate to be 10010000 at po=3, but got %v", binaryString)
	}
}

//getAddressBin is an utility function to obtain a Bin by po
func (tk *testKademlia) getAddressBin(po int) *pot.Bin {
	var theBin *pot.Bin
	tk.defaultIndex.addrs.EachBin(tk.base, Pof, po, func(bin *pot.Bin) bool {
		if bin.ProximityOrder == po {
			theBin = bin
			return false
		} else if bin.ProximityOrder > po {
			return false
		} else {
			return true
		}
	}, true)
	return theBin
}

func bzzAddrToBinary(bzzAddress *BzzAddr) string {
	return byteToBitString(bzzAddress.OAddr[0])
}
