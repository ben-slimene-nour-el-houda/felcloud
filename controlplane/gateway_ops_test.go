package main

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGatewayOpsOnlyAllowsOneActiveOperation(t *testing.T) {
	setupTestDB(t)
	var active atomic.Int32
	var maxActive atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := tryGatewayOp("serial-op"); err != nil {
				return
			}
			current := active.Add(1)
			for {
				prev := maxActive.Load()
				if current <= prev || maxActive.CompareAndSwap(prev, current) {
					break
				}
			}
			time.Sleep(25 * time.Millisecond)
			active.Add(-1)
			releaseGatewayOp("serial-op")
		}()
	}
	close(start)
	wg.Wait()
	if seen := maxActive.Load(); seen != 1 {
		t.Fatalf("expected max concurrent gateway ops to be 1, got %d", seen)
	}
}

func TestGatewayOpsLeaseExpiresForNewHolder(t *testing.T) {
	setupTestDB(t)
	t.Setenv("GATEWAY_OP_TTL_SECONDS", "1")
	if err := tryGatewayOp("holder-a"); err != nil {
		t.Fatalf("acquire first lease: %v", err)
	}
	db, err := getDBConnection()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE gateway_lock SET expires_at = ? WHERE id = 1`, time.Now().Add(-2*time.Second).Unix()); err != nil {
		db.Close()
		t.Fatalf("expire lease: %v", err)
	}
	db.Close()
	releaseGatewayOp("holder-a")
	if err := tryGatewayOp("holder-b"); err != nil {
		t.Fatalf("expected expired lease to allow new holder: %v", err)
	}
	releaseGatewayOp("holder-b")
}

func TestWaitGatewayOpTimesOutWhenBusy(t *testing.T) {
	setupTestDB(t)
	if err := tryGatewayOp("blocking"); err != nil {
		t.Fatalf("acquire blocking lease: %v", err)
	}
	defer releaseGatewayOp("blocking")
	if err := waitGatewayOp("blocking", 50*time.Millisecond); err == nil {
		t.Fatal("expected waitGatewayOp to report busy when the lock remains held")
	}
}
