// +build linux

package node

import (
	"testing"

	"github.com/openshift/sdn/pkg/network/node/ovs"
)

func packetsIn(ovsif ovs.Interface, counts map[string]int, nodeIP string) {
	counts[nodeIP] += 1
	otx := ovsif.NewTransaction()
	otx.DeleteFlows("table=10, tun_src=%s", nodeIP)
	otx.AddFlow("table=10, n_packets=%d, tun_src=%s, actions=goto_table:30", counts[nodeIP], nodeIP)
	err := otx.Commit()
	if err != nil {
		panic("can't happen: " + err.Error())
	}
}

func packetsOut(ovsif ovs.Interface, counts map[uint32]int, nodeIP string, vnid uint32) {
	counts[vnid] += 1
	otx := ovsif.NewTransaction()
	otx.DeleteFlows("table=100, dummy=%s, reg0=%d", nodeIP, vnid)
	otx.AddFlow("table=100, n_packets=%d, dummy=%s, reg0=%d, actions=move:NXM_NX_REG0[]->NXM_NX_TUN_ID[0..31],set_field:%s->tun_dst,output:1", counts[vnid], nodeIP, vnid, nodeIP)
	err := otx.Commit()
	if err != nil {
		panic("can't happen: " + err.Error())
	}
}

func peekUpdate(updates chan struct{}, evm *egressVXLANMonitor) []egressVXLANNode {
	select {
	case <-updates:
		return evm.GetUpdates()
	default:
		return nil
	}
}

func TestEgressVXLANMonitor(t *testing.T) {
	ovsif := ovs.NewFake(Br0)
	ovsif.AddBridge()

	inCounts := make(map[string]int)
	outCounts := make(map[uint32]int)

	packetsIn(ovsif, inCounts, "192.168.1.1")
	packetsOut(ovsif, outCounts, "192.168.1.1", 0x41)
	packetsIn(ovsif, inCounts, "192.168.1.2")
	packetsIn(ovsif, inCounts, "192.168.1.3")
	packetsOut(ovsif, outCounts, "192.168.1.3", 0x43)
	packetsIn(ovsif, inCounts, "192.168.1.4")
	packetsIn(ovsif, inCounts, "192.168.1.5")
	packetsOut(ovsif, outCounts, "192.168.1.5", 0x45)
	packetsOut(ovsif, outCounts, "192.168.1.5", 0x46)
	packetsOut(ovsif, outCounts, "192.168.1.5", 0x47)

	updates := make(chan struct{}, 1)
	evm := newEgressVXLANMonitor(ovsif, nil, updates)
	evm.pollInterval = 0

	evm.AddEgressIP("192.168.1.1", "192.168.1.10")
	evm.AddEgressIP("192.168.1.3", "192.168.1.12")
	evm.AddEgressIP("192.168.1.5", "192.168.1.14")

	// Everything should be fine at startup
	retry := evm.check(false)
	if update := peekUpdate(updates, evm); update != nil {
		t.Fatalf("Initial check showed updated nodes %#v", update)
	}
	if retry {
		t.Fatalf("Initial check requested retry")
	}

	// Send and receive some traffic
	packetsOut(ovsif, outCounts, "192.168.1.1", 0x41)
	packetsIn(ovsif, inCounts, "192.168.1.1")

	packetsIn(ovsif, inCounts, "192.168.1.2")

	packetsOut(ovsif, outCounts, "192.168.1.3", 0x43)
	packetsIn(ovsif, inCounts, "192.168.1.3")

	packetsIn(ovsif, inCounts, "192.168.1.4")

	packetsOut(ovsif, outCounts, "192.168.1.5", 0x45)
	packetsIn(ovsif, inCounts, "192.168.1.5")

	retry = evm.check(false)
	if update := peekUpdate(updates, evm); update != nil {
		t.Fatalf("Check erroneously showed updated nodes %#v", update)
	}
	if retry {
		t.Fatalf("Check erroneously requested retry")
	}

	// Send some more traffic to .3 but don't receive any; this should cause
	// .3 to be noticed as "maybe offline", causing retries. OTOH, receiving
	// traffic on .5 without having sent any should have no effect.
	packetsOut(ovsif, outCounts, "192.168.1.3", 0x43)
	packetsIn(ovsif, inCounts, "192.168.1.5")

	retry = evm.check(false)
	if update := peekUpdate(updates, evm); update != nil {
		t.Fatalf("Check erroneously showed updated nodes %#v", update)
	}
	if !retry {
		t.Fatalf("Check erroneously failed to request retry")
	}
	retry = evm.check(true)
	if update := peekUpdate(updates, evm); update != nil {
		t.Fatalf("Check erroneously showed updated nodes %#v", update)
	}
	if !retry {
		t.Fatalf("Check erroneously failed to request retry")
	}

	// Since we're only doing retries right now, it should ignore this
	packetsOut(ovsif, outCounts, "192.168.1.1", 0x41)

	retry = evm.check(true)
	if update := peekUpdate(updates, evm); update == nil {
		t.Fatalf("Check failed to fail after maxRetries")
	} else if update[0].nodeIP != "192.168.1.3" || !update[0].offline {
		t.Fatalf("Unexpected update nodes %#v", update)
	} else if len(update) > 1 {
		t.Fatalf("Check erroneously showed additional updated nodes %#v", update)
	}

	if update := peekUpdate(updates, evm); update != nil {
		t.Fatalf("Check erroneously showed additional updated nodes %#v", update)
	}
	if retry {
		t.Fatalf("Check erroneously requested retry")
	}
	// If we update .1 now before the next full check, then the monitor should never
	// notice that it was briefly out of sync.
	packetsIn(ovsif, inCounts, "192.168.1.1")
	retry = evm.check(false)
	if update := peekUpdate(updates, evm); update != nil {
		t.Fatalf("Check erroneously showed updated nodes %#v", update)
	}
	if retry {
		t.Fatalf("Check erroneously requested retry")
	}

	// Have .1 lag a bit but then catch up
	packetsOut(ovsif, outCounts, "192.168.1.1", 0x41)
	retry = evm.check(false)
	if update := peekUpdate(updates, evm); update != nil {
		t.Fatalf("Check erroneously showed updated nodes %#v", update)
	}
	if !retry {
		t.Fatalf("Check erroneously failed to request retry")
	}
	packetsIn(ovsif, inCounts, "192.168.1.1")
	retry = evm.check(true)
	if update := peekUpdate(updates, evm); update != nil {
		t.Fatalf("Check erroneously showed updated nodes %#v", update)
	}
	if retry {
		t.Fatalf("Check erroneously requested retry")
	}

	// Now bring back the failed node
	packetsOut(ovsif, outCounts, "192.168.1.3", 0x43)
	packetsIn(ovsif, inCounts, "192.168.1.3")
	retry = evm.check(false)
	if update := peekUpdate(updates, evm); update == nil {
		t.Fatalf("Node failed to recover")
	} else if update[0].nodeIP != "192.168.1.3" || update[0].offline {
		t.Fatalf("Unexpected updated nodes %#v", update)
	} else if len(update) > 1 {
		t.Fatalf("Check erroneously showed additional updated nodes %#v", update)
	}

	if update := peekUpdate(updates, evm); update != nil {
		t.Fatalf("Check erroneously showed additional updated nodes %#v", update)
	}
	if retry {
		t.Fatalf("Check erroneously requested retry")
	}

	// When a node hosts multiple egress IPs, we should notice it failing if *any*
	// IP fails
	packetsOut(ovsif, outCounts, "192.168.1.5", 0x46)
	retry = evm.check(false)
	if update := peekUpdate(updates, evm); update != nil {
		t.Fatalf("Check erroneously showed updated nodes %#v", update)
	}
	if !retry {
		t.Fatalf("Check erroneously failed to request retry")
	}
	retry = evm.check(true)
	if update := peekUpdate(updates, evm); update != nil {
		t.Fatalf("Check erroneously showed updated nodes %#v", update)
	}
	if !retry {
		t.Fatalf("Check erroneously failed to request retry")
	}
	retry = evm.check(true)
	if update := peekUpdate(updates, evm); update == nil {
		t.Fatalf("Check failed to fail after maxRetries")
	} else if update[0].nodeIP != "192.168.1.5" || !update[0].offline {
		t.Fatalf("Unexpected update nodes %#v", update)
	} else if len(update) > 1 {
		t.Fatalf("Check erroneously showed additional updated nodes %#v", update)
	}

	if update := peekUpdate(updates, evm); update != nil {
		t.Fatalf("Check erroneously showed additional updated nodes %#v", update)
	}
	if retry {
		t.Fatalf("Check erroneously requested retry")
	}

	packetsIn(ovsif, inCounts, "192.168.1.5")
	retry = evm.check(false)
	if update := peekUpdate(updates, evm); update == nil {
		t.Fatalf("Node failed to recover")
	} else if update[0].nodeIP != "192.168.1.5" || update[0].offline {
		t.Fatalf("Unexpected updated nodes %#v", update)
	} else if len(update) > 1 {
		t.Fatalf("Check erroneously showed additional updated nodes %#v", update)
	}

	if update := peekUpdate(updates, evm); update != nil {
		t.Fatalf("Check erroneously showed additional updated nodes %#v", update)
	}
	if retry {
		t.Fatalf("Check erroneously requested retry")
	}

	// Ensure evm.check doesn't block when evm.updates is full.
	// https://bugzilla.redhat.com/show_bug.cgi?id=1824203

	evm.updates <- struct{}{} // make updates buffer artificially full

	// Send some traffic out but don't receive any.
	// Do it sequentially in order to ensure it's OK for it to happen
	// in two different iterations without the updates being consumed
	for _, node := range []string{"192.168.1.1", "192.168.1.3"} {
		retry = false
		// Redundant but anyway...
		if len(evm.updates) != 1 {
			t.Fatal("Unexpected flush of the updates channel")
		}
		for i := 0; i < 3; i++ {
			packetsOut(ovsif, outCounts, node, 0x41)
			retry = evm.check(retry)
		}
	}

	update := peekUpdate(updates, evm)
	if update == nil {
		t.Fatalf("Nodes failed to go offline")
	} else if len(update) != 2 {
		t.Fatalf("Check return wrong number of updates %#v", update)
	}

	// GetUpdates returns the updates in a random order
	if !(update[0].nodeIP == "192.168.1.1" && update[1].nodeIP == "192.168.1.3") &&
		!(update[0].nodeIP == "192.168.1.3" && update[1].nodeIP == "192.168.1.1") {
		t.Fatalf("Expected one update for 192.168.1.1 and one for 192.168.1.3: %#v", update)
	} else if !update[0].offline {
		t.Fatalf("%s unexpectedly online: %#v", update[0].nodeIP, update)
	} else if !update[1].offline {
		t.Fatalf("%s unexpectedly online: %#v", update[1].nodeIP, update)
	}

	evm.AddEgressIP("192.168.1.5", "192.168.1.16")
	packetsOut(ovsif, outCounts, "192.168.1.1", 0x46)

	retry = evm.check(false)
	if retry {
		t.Fatalf("Check erroneously requested retry")
	}

	evm.RemoveEgressIP("192.168.1.3", "192.168.1.12")
	evm.RemoveEgressIP("192.168.1.5", "192.168.1.14")
	evm.RemoveEgressIP("192.168.1.1", "192.168.1.10")
	packetsOut(ovsif, outCounts, "192.168.1.1", 0x46)

	// At this point we only monitor 192.168.1.5
	// (as it will still have one egress IP left -
	// 192.168.1.16 - and is considered online),
	// so check that increasing packets on the
	// other nodes have no effect.
	retry = evm.check(false)
	if retry {
		t.Fatalf("Check erroneously requested retry")
	}

	packetsOut(ovsif, outCounts, "192.168.1.5", 0x46)

	// Check that we're still monitoring 192.168.1.5
	retry = evm.check(false)
	if !retry {
		t.Fatalf("Check erroneously failed to request retry")
	}

	evm.RemoveEgressIP("192.168.1.5", "192.168.1.16")
	packetsOut(ovsif, outCounts, "192.168.1.5", 0x46)

	// At this point we are not monitoring any node
	// so there should be no more retries at all, even if
	// 192.168.1.5 is still considered online and should
	// trigger a retry normally.
	retry = evm.check(false)
	if retry {
		t.Fatalf("Check erroneously requested retry")
	}
}
