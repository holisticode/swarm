package network

import (
	"fmt"
	"io"

	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/holisticode/swarm/log"
	"github.com/holisticode/swarm/p2p/protocols"
)

// ENRAddrEntry is the entry type to store the bzz key in the enode
type ENRAddrEntry struct {
	data []byte
}

func NewENRAddrEntry(addr []byte) *ENRAddrEntry {
	return &ENRAddrEntry{
		data: addr,
	}
}

func (b ENRAddrEntry) Address() []byte {
	return b.data
}

// ENRKey implements enr.Entry
func (b ENRAddrEntry) ENRKey() string {
	return "bzzkey"
}

// EncodeRLP implements rlp.Encoder
func (b ENRAddrEntry) EncodeRLP(w io.Writer) error {
	log.Debug("in encoderlp", "b", b, "p", fmt.Sprintf("%p", &b))
	return rlp.Encode(w, &b.data)
}

// DecodeRLP implements rlp.Decoder
func (b *ENRAddrEntry) DecodeRLP(s *rlp.Stream) error {
	byt, err := s.Bytes()
	if err != nil {
		return err
	}
	b.data = byt
	log.Debug("in decoderlp", "b", b, "p", fmt.Sprintf("%p", &b))
	return nil
}

type ENRBootNodeEntry bool

func (b ENRBootNodeEntry) ENRKey() string {
	return "bzzbootnode"
}

func getENRBzzPeer(p *p2p.Peer, rw p2p.MsgReadWriter, spec *protocols.Spec) *BzzPeer {
	var bootnode ENRBootNodeEntry

	// retrieve the ENR Record data
	record := p.Node().Record()
	record.Load(&bootnode)

	// get the address; separate function as long as we need swarm/network:NewBzzAddrFromEnode() to call it
	addr := getENRBzzAddr(p.Node())

	// build the peer using the retrieved data
	return &BzzPeer{
		Peer:    protocols.NewPeer(p, rw, spec),
		BzzAddr: addr,
	}
}

func getENRBzzAddr(nod *enode.Node) *BzzAddr {
	var addr ENRAddrEntry

	record := nod.Record()
	record.Load(&addr)

	return NewBzzAddr(addr.data, []byte(nod.String()))
}
