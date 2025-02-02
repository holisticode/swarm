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

package localstore

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/holisticode/swarm/chunk"
	chunktesting "github.com/holisticode/swarm/chunk/testing"
	"github.com/holisticode/swarm/shed"
	"github.com/syndtr/goleveldb/leveldb"
)

func init() {
	// Some of the tests in localstore package rely on the same ordering of
	// items uploaded or accessed compared to the ordering of items in indexes
	// that contain StoreTimestamp or AccessTimestamp in keys. In tests
	// where the same order is required from the database as the order
	// in which chunks are put or accessed, if the StoreTimestamp or
	// AccessTimestamp are the same for two or more sequential items
	// their order in database will be based on the chunk address value,
	// in which case the ordering of items/chunks stored in a test slice
	// will not be the same. To ensure the same ordering in database on such
	// indexes on windows systems, an additional short sleep is added to
	// the now function.
	if runtime.GOOS == "windows" {
		setNow(func() int64 {
			time.Sleep(time.Microsecond)
			return time.Now().UTC().UnixNano()
		})
	}
}

// TestDB validates if the chunk can be uploaded and
// correctly retrieved.
func TestDB(t *testing.T) {
	db, cleanupFunc := newTestDB(t, nil)
	defer cleanupFunc()

	ch := generateTestRandomChunk()

	_, err := db.Put(context.Background(), chunk.ModePutUpload, ch)
	if err != nil {
		t.Fatal(err)
	}

	got, err := db.Get(context.Background(), chunk.ModeGetRequest, ch.Address())
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got.Address(), ch.Address()) {
		t.Errorf("got address %x, want %x", got.Address(), ch.Address())
	}
	if !bytes.Equal(got.Data(), ch.Data()) {
		t.Errorf("got data %x, want %x", got.Data(), ch.Data())
	}
}

// TestDB_updateGCSem tests maxParallelUpdateGC limit.
// This test temporary sets the limit to a low number,
// makes updateGC function execution time longer by
// setting a custom testHookUpdateGC function with a sleep
// and a count current and maximal number of goroutines.
func TestDB_updateGCSem(t *testing.T) {
	updateGCSleep := time.Second
	var count int
	var max int
	var mu sync.Mutex
	defer setTestHookUpdateGC(func() {
		mu.Lock()
		// add to the count of current goroutines
		count++
		if count > max {
			// set maximal detected numbers of goroutines
			max = count
		}
		mu.Unlock()

		// wait for some time to ensure multiple parallel goroutines
		time.Sleep(updateGCSleep)

		mu.Lock()
		count--
		mu.Unlock()
	})()

	defer func(m int) { maxParallelUpdateGC = m }(maxParallelUpdateGC)
	maxParallelUpdateGC = 3

	db, cleanupFunc := newTestDB(t, nil)
	defer cleanupFunc()

	ch := generateTestRandomChunk()

	_, err := db.Put(context.Background(), chunk.ModePutUpload, ch)
	if err != nil {
		t.Fatal(err)
	}

	// get more chunks then maxParallelUpdateGC
	// in time shorter then updateGCSleep
	for i := 0; i < 5; i++ {
		_, err = db.Get(context.Background(), chunk.ModeGetRequest, ch.Address())
		if err != nil {
			t.Fatal(err)
		}
	}

	if max != maxParallelUpdateGC {
		t.Errorf("got max %v, want %v", max, maxParallelUpdateGC)
	}
}

// newTestDB is a helper function that constructs a
// temporary database and returns a cleanup function that must
// be called to remove the data.
func newTestDB(t testing.TB, o *Options) (db *DB, cleanupFunc func()) {
	t.Helper()

	dir, err := ioutil.TempDir("", "localstore-test")
	if err != nil {
		t.Fatal(err)
	}
	cleanupFunc = func() { os.RemoveAll(dir) }
	baseKey := make([]byte, 32)
	if _, err := rand.Read(baseKey); err != nil {
		t.Fatal(err)
	}
	db, err = New(dir, baseKey, o)
	if err != nil {
		cleanupFunc()
		t.Fatal(err)
	}
	cleanupFunc = func() {
		err := db.Close()
		if err != nil {
			t.Error(err)
		}
		os.RemoveAll(dir)
	}
	return db, cleanupFunc
}

var (
	generateTestRandomChunk  = chunktesting.GenerateTestRandomChunk
	generateTestRandomChunks = chunktesting.GenerateTestRandomChunks
)

// chunkAddresses return chunk addresses of provided chunks.
func chunkAddresses(chunks []chunk.Chunk) []chunk.Address {
	addrs := make([]chunk.Address, len(chunks))
	for i, ch := range chunks {
		addrs[i] = ch.Address()
	}
	return addrs
}

// Standard test cases to validate multi chunk operations.
var multiChunkTestCases = []struct {
	name  string
	count int
}{
	{
		name:  "one",
		count: 1,
	},
	{
		name:  "two",
		count: 2,
	},
	{
		name:  "eight",
		count: 8,
	},
	{
		name:  "hundred",
		count: 100,
	},
	{
		name:  "thousand",
		count: 1000,
	},
}

// TestGenerateTestRandomChunk validates that
// generateTestRandomChunk returns random data by comparing
// two generated chunks.
func TestGenerateTestRandomChunk(t *testing.T) {
	c1 := generateTestRandomChunk()
	c2 := generateTestRandomChunk()
	addrLen := len(c1.Address())
	if addrLen != 32 {
		t.Errorf("first chunk address length %v, want %v", addrLen, 32)
	}
	dataLen := len(c1.Data())
	if dataLen != chunk.DefaultSize {
		t.Errorf("first chunk data length %v, want %v", dataLen, chunk.DefaultSize)
	}
	addrLen = len(c2.Address())
	if addrLen != 32 {
		t.Errorf("second chunk address length %v, want %v", addrLen, 32)
	}
	dataLen = len(c2.Data())
	if dataLen != chunk.DefaultSize {
		t.Errorf("second chunk data length %v, want %v", dataLen, chunk.DefaultSize)
	}
	if bytes.Equal(c1.Address(), c2.Address()) {
		t.Error("fake chunks addresses do not differ")
	}
	if bytes.Equal(c1.Data(), c2.Data()) {
		t.Error("fake chunks data bytes do not differ")
	}
}

// newRetrieveIndexesTest returns a test function that validates if the right
// chunk values are in the retrieval indexes
func newRetrieveIndexesTest(db *DB, chunk chunk.Chunk, storeTimestamp, accessTimestamp int64) func(t *testing.T) {
	return func(t *testing.T) {
		t.Helper()

		item, err := db.retrievalDataIndex.Get(addressToItem(chunk.Address()))
		if err != nil {
			t.Fatal(err)
		}
		validateItem(t, item, chunk.Address(), chunk.Data(), storeTimestamp, 0)

		// access index should not be set
		wantErr := leveldb.ErrNotFound
		item, err = db.retrievalAccessIndex.Get(addressToItem(chunk.Address()))
		if err != wantErr {
			t.Errorf("got error %v, want %v", err, wantErr)
		}
	}
}

// newRetrieveIndexesTestWithAccess returns a test function that validates if the right
// chunk values are in the retrieval indexes when access time must be stored.
func newRetrieveIndexesTestWithAccess(db *DB, ch chunk.Chunk, storeTimestamp, accessTimestamp int64) func(t *testing.T) {
	return func(t *testing.T) {
		t.Helper()

		item, err := db.retrievalDataIndex.Get(addressToItem(ch.Address()))
		if err != nil {
			t.Fatal(err)
		}
		validateItem(t, item, ch.Address(), ch.Data(), storeTimestamp, 0)

		if accessTimestamp > 0 {
			item, err = db.retrievalAccessIndex.Get(addressToItem(ch.Address()))
			if err != nil {
				t.Fatal(err)
			}
			validateItem(t, item, ch.Address(), nil, 0, accessTimestamp)
		}
	}
}

// newPullIndexTest returns a test function that validates if the right
// chunk values are in the pull index.
func newPullIndexTest(db *DB, ch chunk.Chunk, binID uint64, wantError error) func(t *testing.T) {
	return func(t *testing.T) {
		t.Helper()

		item, err := db.pullIndex.Get(shed.Item{
			Address: ch.Address(),
			BinID:   binID,
		})
		if err != wantError {
			t.Errorf("got error %v, want %v", err, wantError)
		}
		if err == nil {
			validateItem(t, item, ch.Address(), nil, 0, 0)
		}
	}
}

// newPinIndexTest returns a test function that validates if the right
// chunk values are in the pin index.
func newPinIndexTest(db *DB, ch chunk.Chunk, wantError error) func(t *testing.T) {
	return func(t *testing.T) {
		t.Helper()

		item, err := db.pinIndex.Get(shed.Item{
			Address: ch.Address(),
		})
		if err != wantError {
			t.Errorf("got error %v, want %v", err, wantError)
		}
		if err == nil {
			validateItem(t, item, ch.Address(), nil, 0, 0)
		}
	}
}

// newPushIndexTest returns a test function that validates if the right
// chunk values are in the push index.
func newPushIndexTest(db *DB, ch chunk.Chunk, storeTimestamp int64, wantError error) func(t *testing.T) {
	return func(t *testing.T) {
		t.Helper()

		item, err := db.pushIndex.Get(shed.Item{
			Address:        ch.Address(),
			StoreTimestamp: storeTimestamp,
		})
		if err != wantError {
			t.Errorf("got error %v, want %v", err, wantError)
		}
		if err == nil {
			validateItem(t, item, ch.Address(), nil, storeTimestamp, 0)
		}
	}
}

// newGCIndexTest returns a test function that validates if the right
// chunk values are in the GC index.
func newGCIndexTest(db *DB, chunk chunk.Chunk, storeTimestamp, accessTimestamp int64, binID uint64, wantError error) func(t *testing.T) {
	return func(t *testing.T) {
		t.Helper()

		item, err := db.gcIndex.Get(shed.Item{
			Address:         chunk.Address(),
			BinID:           binID,
			AccessTimestamp: accessTimestamp,
		})
		if err != wantError {
			t.Errorf("got error %v, want %v", err, wantError)
		}
		if err == nil {
			validateItem(t, item, chunk.Address(), nil, 0, accessTimestamp)
		}
	}
}

// newItemsCountTest returns a test function that validates if
// an index contains expected number of key/value pairs.
func newItemsCountTest(i shed.Index, want int) func(t *testing.T) {
	return func(t *testing.T) {
		t.Helper()

		var c int
		err := i.Iterate(func(item shed.Item) (stop bool, err error) {
			c++
			return
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if c != want {
			t.Errorf("got %v items in index, want %v", c, want)
		}
	}
}

// newIndexGCSizeTest retruns a test function that validates if DB.gcSize
// value is the same as the number of items in DB.gcIndex.
func newIndexGCSizeTest(db *DB) func(t *testing.T) {
	return func(t *testing.T) {
		t.Helper()

		var want uint64
		err := db.gcIndex.Iterate(func(item shed.Item) (stop bool, err error) {
			want++
			return
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		got, err := db.gcSize.Get()
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("got gc size %v, want %v", got, want)
		}
	}
}

func tagSyncedCounterTest(t *testing.T, count int, mode chunk.ModeSet, tag *chunk.Tag) {
	c, _, err := tag.Status(chunk.StateSynced)
	if err != nil {
		t.Fatal(err)
	}
	doCheck := func(c int) {
		if c != count {
			t.Fatalf("synced count mismatch. got %d want %d", c, count)
		}
	}

	// this should not be invoked always
	if mode == chunk.ModeSetSyncPull && tag.Anonymous {
		doCheck(int(c))
	}

	if mode == chunk.ModeSetSyncPush && !tag.Anonymous {
		doCheck(int(c))
	}
}

// testIndexChunk embeds storageChunk with additional data that is stored
// in database. It is used for index values validations.
type testIndexChunk struct {
	chunk.Chunk
	binID uint64
}

// testItemsOrder tests the order of chunks in the index. If sortFunc is not nil,
// chunks will be sorted with it before validation.
func testItemsOrder(t *testing.T, i shed.Index, chunks []testIndexChunk, sortFunc func(i, j int) (less bool)) {
	t.Helper()

	newItemsCountTest(i, len(chunks))(t)

	if sortFunc != nil {
		sort.Slice(chunks, sortFunc)
	}

	var cursor int
	err := i.Iterate(func(item shed.Item) (stop bool, err error) {
		want := chunks[cursor].Address()
		got := item.Address
		if !bytes.Equal(got, want) {
			return true, fmt.Errorf("got address %x at position %v, want %x", got, cursor, want)
		}
		cursor++
		return false, nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
}

// validateItem is a helper function that checks Item values.
func validateItem(t *testing.T, item shed.Item, address, data []byte, storeTimestamp, accessTimestamp int64) {
	t.Helper()

	if !bytes.Equal(item.Address, address) {
		t.Errorf("got item address %x, want %x", item.Address, address)
	}
	if !bytes.Equal(item.Data, data) {
		t.Errorf("got item data %x, want %x", item.Data, data)
	}
	if item.StoreTimestamp != storeTimestamp {
		t.Errorf("got item store timestamp %v, want %v", item.StoreTimestamp, storeTimestamp)
	}
	if item.AccessTimestamp != accessTimestamp {
		t.Errorf("got item access timestamp %v, want %v", item.AccessTimestamp, accessTimestamp)
	}
}

// setNow replaces now function and
// returns a function that will reset it to the
// value before the change.
func setNow(f func() int64) (reset func()) {
	current := now
	reset = func() { now = current }
	now = f
	return reset
}

// TestSetNow tests if setNow function changes now function
// correctly and if its reset function resets the original function.
func TestSetNow(t *testing.T) {
	// set the current function after the test finishes
	defer func(f func() int64) { now = f }(now)

	// expected value for the unchanged function
	var original int64 = 1
	// expected value for the changed function
	var changed int64 = 2

	// define the original (unchanged) functions
	now = func() int64 {
		return original
	}

	// get the time
	got := now()

	// test if got variable is set correctly
	if got != original {
		t.Errorf("got now value %v, want %v", got, original)
	}

	// set the new function
	reset := setNow(func() int64 {
		return changed
	})

	// get the time
	got = now()

	// test if got variable is set correctly to changed value
	if got != changed {
		t.Errorf("got hook value %v, want %v", got, changed)
	}

	// set the function to the original one
	reset()

	// get the time
	got = now()

	// test if got variable is set correctly to original value
	if got != original {
		t.Errorf("got hook value %v, want %v", got, original)
	}
}

func testIndexCounts(t *testing.T, pushIndex, pullIndex, gcIndex, gcExcludeIndex, pinIndex, retrievalDataIndex, retrievalAccessIndex int, indexInfo map[string]int) {
	t.Helper()
	if indexInfo["pushIndex"] != pushIndex {
		t.Fatalf("pushIndex count mismatch. got %d want %d", indexInfo["pushIndex"], pushIndex)
	}

	if indexInfo["pullIndex"] != pullIndex {
		t.Fatalf("pullIndex count mismatch. got %d want %d", indexInfo["pullIndex"], pullIndex)
	}

	if indexInfo["gcIndex"] != gcIndex {
		t.Fatalf("gcIndex count mismatch. got %d want %d", indexInfo["gcIndex"], gcIndex)
	}

	if indexInfo["gcExcludeIndex"] != gcExcludeIndex {
		t.Fatalf("gcExcludeIndex count mismatch. got %d want %d", indexInfo["gcExcludeIndex"], gcExcludeIndex)
	}

	if indexInfo["pinIndex"] != pinIndex {
		t.Fatalf("pinIndex count mismatch. got %d want %d", indexInfo["pinIndex"], pinIndex)
	}

	if indexInfo["retrievalDataIndex"] != retrievalDataIndex {
		t.Fatalf("retrievalDataIndex count mismatch. got %d want %d", indexInfo["retrievalDataIndex"], retrievalDataIndex)
	}

	if indexInfo["retrievalAccessIndex"] != retrievalAccessIndex {
		t.Fatalf("retrievalAccessIndex count mismatch. got %d want %d", indexInfo["retrievalAccessIndex"], retrievalAccessIndex)
	}
}

// TestDBDebugIndexes tests that the index counts are correct for the
// index debug function
func TestDBDebugIndexes(t *testing.T) {
	db, cleanupFunc := newTestDB(t, nil)
	defer cleanupFunc()

	uploadTimestamp := time.Now().UTC().UnixNano()
	defer setNow(func() (t int64) {
		return uploadTimestamp
	})()

	ch := generateTestRandomChunk()

	_, err := db.Put(context.Background(), chunk.ModePutUpload, ch)
	if err != nil {
		t.Fatal(err)
	}

	indexCounts, err := db.DebugIndices()
	if err != nil {
		t.Fatal(err)
	}

	// for reference: testIndexCounts(t *testing.T, pushIndex, pullIndex, gcIndex, gcExcludeIndex, pinIndex, retrievalDataIndex, retrievalAccessIndex int, indexInfo map[string]int)
	testIndexCounts(t, 1, 1, 0, 0, 0, 1, 0, indexCounts)

	// set the chunk for pinning and expect the index count to grow
	err = db.Set(context.Background(), chunk.ModeSetPin, ch.Address())
	if err != nil {
		t.Fatal(err)
	}

	indexCounts, err = db.DebugIndices()
	if err != nil {
		t.Fatal(err)
	}

	// assert that there's a pin and gc exclude entry now
	testIndexCounts(t, 1, 1, 0, 1, 1, 1, 0, indexCounts)

	// set the chunk as accessed and expect the access index to grow
	err = db.Set(context.Background(), chunk.ModeSetAccess, ch.Address())
	if err != nil {
		t.Fatal(err)
	}
	indexCounts, err = db.DebugIndices()
	if err != nil {
		t.Fatal(err)
	}

	// assert that there's a pin and gc exclude entry now
	testIndexCounts(t, 1, 1, 0, 1, 1, 1, 1, indexCounts)

}
