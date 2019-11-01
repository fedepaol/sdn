package master

import (
	"fmt"
	"net"
	"testing"
)

func newSubnetAllocator(clusterCIDR string, hostBits uint32) (*SubnetAllocator, error) {
	sna := NewSubnetAllocator()
	err := sna.AddNetworkRange(clusterCIDR, hostBits)
	return sna, err
}

func networkID(n int) string {
	if n == -1 {
		return "network"
	} else {
		return fmt.Sprintf("network %d", n)
	}
}

func allocateExpected(sna *SubnetAllocator, n int, expected string) error {
	sn, err := sna.AllocateNetwork()
	if err != nil {
		return fmt.Errorf("failed to allocate %s (%s): %v", networkID(n), expected, err)
	}
	if sn != expected {
		return fmt.Errorf("failed to allocate %s: expected %s, got %s", networkID(n), expected, sn)
	}
	return nil
}

func allocateNotExpected(sna *SubnetAllocator, n int) error {
	if sn, err := sna.AllocateNetwork(); err == nil {
		return fmt.Errorf("unexpectedly succeeded in allocating %s (sn=%s)", networkID(n), sn)
	} else if err != ErrSubnetAllocatorFull {
		return fmt.Errorf("returned error was not ErrSubnetAllocatorFull (%v)", err)
	}
	return nil
}

// 10.1.SSSSSSSS.HHHHHHHH
func TestAllocateSubnet(t *testing.T) {
	sna, err := newSubnetAllocator("10.1.0.0/16", 8)
	if err != nil {
		t.Fatal("Failed to initialize subnet allocator: ", err)
	}

	for n := 0; n < 256; n++ {
		if err := allocateExpected(sna, n, fmt.Sprintf("10.1.%d.0/24", n)); err != nil {
			t.Fatal(err)
		}
	}
	if err := allocateNotExpected(sna, 256); err != nil {
		t.Fatal(err)
	}
}

// 10.1.SSSSSSHH.HHHHHHHH
func TestAllocateSubnetLargeHostBits(t *testing.T) {
	sna, err := newSubnetAllocator("10.1.0.0/16", 10)
	if err != nil {
		t.Fatal("Failed to initialize subnet allocator: ", err)
	}

	for n := 0; n < 64; n++ {
		if err := allocateExpected(sna, n, fmt.Sprintf("10.1.%d.0/22", n*4)); err != nil {
			t.Fatal(err)
		}
	}
	if err := allocateNotExpected(sna, 64); err != nil {
		t.Fatal(err)
	}
}

// 10.1.SSSSSSSS.SSHHHHHH
func TestAllocateSubnetLargeSubnetBits(t *testing.T) {
	sna, err := newSubnetAllocator("10.1.0.0/16", 6)
	if err != nil {
		t.Fatal("Failed to initialize subnet allocator: ", err)
	}

	// for IPv4, we tweak the allocation order and expect to see all of the ".0"
	// networks before any non-".0" network
	for n := 0; n < 256; n++ {
		if err = allocateExpected(sna, n, fmt.Sprintf("10.1.%d.0/26", n)); err != nil {
			t.Fatal(err)
		}
	}
	for n := 0; n < 256; n++ {
		if err = allocateExpected(sna, n+256, fmt.Sprintf("10.1.%d.64/26", n)); err != nil {
			t.Fatal(err)
		}
	}
	if err = allocateExpected(sna, 512, "10.1.0.128/26"); err != nil {
		t.Fatal(err)
	}

	sna.ranges[0].next = 1023
	if err = allocateExpected(sna, -1, "10.1.255.192/26"); err != nil {
		t.Fatal(err)
	}
	// Next allocation should wrap around and get the next unallocated network (513)
	if err = allocateExpected(sna, -1, "10.1.1.128/26"); err != nil {
		t.Fatalf("After wraparound: %v", err)
	}
}

// 10.000000SS.SSSSSSHH.HHHHHHHH
func TestAllocateSubnetOverlapping(t *testing.T) {
	sna, err := newSubnetAllocator("10.0.0.0/14", 10)
	if err != nil {
		t.Fatal("Failed to initialize subnet allocator: ", err)
	}

	for n := 0; n < 4; n++ {
		if err = allocateExpected(sna, n, fmt.Sprintf("10.%d.0.0/22", n)); err != nil {
			t.Fatal(err)
		}
	}
	for n := 0; n < 4; n++ {
		if err = allocateExpected(sna, n+4, fmt.Sprintf("10.%d.4.0/22", n)); err != nil {
			t.Fatal(err)
		}
	}
	if err := allocateExpected(sna, 8, "10.0.8.0/22"); err != nil {
		t.Fatal(err)
	}

	sna.ranges[0].next = 255
	if err := allocateExpected(sna, -1, "10.3.252.0/22"); err != nil {
		t.Fatal(err)
	}
	if err := allocateExpected(sna, -1, "10.1.8.0/22"); err != nil {
		t.Fatalf("After wraparound: %v", err)
	}
}

// 10.1.HHHHHHHH.HHHHHHHH
func TestAllocateSubnetNoSubnetBits(t *testing.T) {
	sna, err := newSubnetAllocator("10.1.0.0/16", 16)
	if err != nil {
		t.Fatal("Failed to initialize subnet allocator: ", err)
	}

	if err := allocateExpected(sna, 0, "10.1.0.0/16"); err != nil {
		t.Fatal(err)
	}
	if err := allocateNotExpected(sna, 1); err != nil {
		t.Fatal(err)
	}
}

func TestAllocateSubnetInvalidHostBitsOrCIDR(t *testing.T) {
	_, err := newSubnetAllocator("10.1.0.0/16", 18)
	if err == nil {
		t.Fatal("Unexpectedly succeeded in initializing subnet allocator")
	}

	_, err = newSubnetAllocator("10.1.0.0/16", 0)
	if err == nil {
		t.Fatal("Unexpectedly succeeded in initializing subnet allocator")
	}

	_, err = newSubnetAllocator("10.1.0.0/33", 16)
	if err == nil {
		t.Fatal("Unexpectedly succeeded in initializing subnet allocator")
	}
}

func TestMarkAllocatedNetwork(t *testing.T) {
	sna, err := newSubnetAllocator("10.1.0.0/16", 14)
	if err != nil {
		t.Fatal("Failed to initialize IP allocator: ", err)
	}

	allocSubnets := make([]string, 4)
	for i := 0; i < 4; i++ {
		if allocSubnets[i], err = sna.AllocateNetwork(); err != nil {
			t.Fatal("Failed to allocate network: ", err)
		}
	}

	if sn, err := sna.AllocateNetwork(); err == nil {
		t.Fatalf("Unexpectedly succeeded in allocating network (sn=%s)", sn)
	}
	if err := sna.ReleaseNetwork(allocSubnets[2]); err != nil {
		t.Fatalf("Failed to release the subnet (allocSubnets[2]=%s): %v", allocSubnets[2], err)
	}
	for i := 0; i < 2; i++ {
		if err := sna.MarkAllocatedNetwork(allocSubnets[2]); err != nil {
			t.Fatalf("Failed to mark allocated subnet (allocSubnets[2]=%s): %v", allocSubnets[2], err)
		}
	}
	if sn, err := sna.AllocateNetwork(); err == nil {
		t.Fatalf("Unexpectedly succeeded in allocating network (sn=%s)", sn)
	}

	// Test subnet that does not belong to network
	sn := "10.2.3.4/24"
	if err := sna.MarkAllocatedNetwork(sn); err == nil {
		t.Fatalf("Unexpectedly succeeded in marking allocated subnet that doesn't belong to network (sn=%s)", sn)
	}
}

func TestAllocateReleaseSubnet(t *testing.T) {
	sna, err := newSubnetAllocator("10.1.0.0/16", 14)
	if err != nil {
		t.Fatal("Failed to initialize IP allocator: ", err)
	}

	var releaseSn string

	for i := 0; i < 4; i++ {
		sn, err := sna.AllocateNetwork()
		if err != nil {
			t.Fatal("Failed to allocate network: ", err)
		}
		if sn != fmt.Sprintf("10.1.%d.0/18", i*64) {
			t.Fatalf("Did not get expected subnet (i=%d, sn=%s)", i, sn)
		}
		if i == 2 {
			releaseSn = sn
		}
	}

	sn, err := sna.AllocateNetwork()
	if err == nil {
		t.Fatalf("Unexpectedly succeeded in allocating network (sn=%s)", sn)
	}

	if err := sna.ReleaseNetwork(releaseSn); err != nil {
		t.Fatalf("Failed to release the subnet (releaseSn=%s): %v", releaseSn, err)
	}

	sn, err = sna.AllocateNetwork()
	if err != nil {
		t.Fatal("Failed to allocate network: ", err)
	}
	if sn != releaseSn {
		t.Fatalf("Did not get expected subnet (sn=%s)", sn)
	}

	sn, err = sna.AllocateNetwork()
	if err == nil {
		t.Fatalf("Unexpectedly succeeded in allocating network (sn=%s)", sn)
	}
}

func TestMultipleSubnets(t *testing.T) {
	sna, err := newSubnetAllocator("10.1.0.0/16", 14)
	if err != nil {
		t.Fatal("Failed to initialize IP allocator: ", err)
	}
	err = sna.AddNetworkRange("10.2.0.0/16", 14)
	if err != nil {
		t.Fatal("Failed to add network range: ", err)
	}

	for i := 0; i < 4; i++ {
		if err := allocateExpected(sna, i, fmt.Sprintf("10.1.%d.0/18", i*64)); err != nil {
			t.Fatal(err)
		}
	}

	for i := 0; i < 4; i++ {
		if err := allocateExpected(sna, i+4, fmt.Sprintf("10.2.%d.0/18", i*64)); err != nil {
			t.Fatal(err)
		}
	}

	if err := allocateNotExpected(sna, 8); err != nil {
		t.Fatal(err)
	}

	if err := sna.ReleaseNetwork("10.1.128.0/18"); err != nil {
		t.Fatalf("Failed to release the subnet 10.1.128.0/18: %v", err)
	}
	if err := sna.ReleaseNetwork("10.2.128.0/18"); err != nil {
		t.Fatalf("Failed to release the subnet 10.2.128.0/18: %v", err)
	}

	if err := allocateExpected(sna, -1, "10.1.128.0/18"); err != nil {
		t.Fatal(err)
	}
	if err := allocateExpected(sna, -1, "10.2.128.0/18"); err != nil {
		t.Fatal(err)
	}

	if err := allocateNotExpected(sna, -1); err != nil {
		t.Fatal(err)
	}
}

func TestIPUint32Conversion(t *testing.T) {
	ip := net.ParseIP("10.1.2.3")
	if ip == nil {
		t.Fatal("Failed to parse IP")
	}

	u := IPToUint32(ip)
	t.Log(u)
	ip2 := Uint32ToIP(u)
	t.Log(ip2)

	if !ip2.Equal(ip) {
		t.Fatal("Conversion back and forth failed")
	}
}
