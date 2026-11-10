// repair-blockstore rewrites the missing blockStore metadata key in a
// CometBFT blockstore.db LevelDB directory.
//
// When CometBFT crashes mid-write the "blockStore" key (which records
// the store's Base and Height) can be lost while the actual block/commit
// data remains intact. Without this key h.store.Height() returns 0,
// triggering an immediate assertAppHashEqualsOneFromState panic on the
// next startup even though all data is fine.
//
// Usage:
//
//	repair-blockstore <blockstore.db path> <height>
//
// Example:
//
//	repair-blockstore ~/.chakram-node/comet/data/blockstore.db 25000
package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/syndtr/goleveldb/leveldb"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: repair-blockstore <blockstore.db path> <height>")
		os.Exit(1)
	}

	dbPath := os.Args[1]
	height, err := strconv.ParseInt(os.Args[2], 10, 64)
	if err != nil || height <= 0 {
		fmt.Fprintf(os.Stderr, "invalid height %q: %v\n", os.Args[2], err)
		os.Exit(1)
	}

	db, err := leveldb.OpenFile(dbPath, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open %s: %v\n", dbPath, err)
		os.Exit(1)
	}
	defer db.Close()

	// Read existing value so we can show what we're overwriting (if anything).
	existing, _ := db.Get([]byte("blockStore"), nil)
	if existing != nil {
		fmt.Printf("existing blockStore key: %x\n", existing)
	} else {
		fmt.Println("blockStore key not found — writing fresh")
	}

	// Protobuf encoding for cmtstore.BlockStoreState{Base: 1, Height: height}
	//   message BlockStoreState { int64 base = 1; int64 height = 2; }
	// field 1 (base = 1):   tag 0x08, varint 1 = 0x01
	// field 2 (height = N): tag 0x10, varint N
	val := appendVarintField(nil, 1, 1)      // base = 1
	val = appendVarintField(val, 2, height)  // height = N

	if err := db.Put([]byte("blockStore"), val, nil); err != nil {
		fmt.Fprintf(os.Stderr, "put: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("repaired: blockStore = {base:1, height:%d}  bytes: %x\n", height, val)
}

// appendVarintField appends a protobuf varint field (wire type 0) to dst.
func appendVarintField(dst []byte, fieldNum int, v int64) []byte {
	tag := uint64(fieldNum) << 3 // wire type 0
	dst = appendUvarint(dst, tag)
	dst = appendUvarint(dst, uint64(v))
	return dst
}

func appendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}
