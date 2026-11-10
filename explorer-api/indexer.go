package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

type Indexer struct {
	db        *Database
	geth      *GethClient
	comet     *CometClient
	log       *slog.Logger
	fromBlock int64
}

func (idx *Indexer) Run(ctx context.Context) {
	last := idx.db.GetLastIndexedBlock()
	if last == 0 && idx.fromBlock > 1 {
		last = idx.fromBlock - 1
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		head, err := idx.geth.BlockNumber()
		if err != nil {
			idx.log.Warn("indexer: geth unavailable", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
			continue
		}

		if last >= head {
			select {
			case <-ctx.Done():
				return
			case <-time.After(1 * time.Second):
			}
			continue
		}

		next := last + 1
		if err := idx.indexBlock(ctx, next); err != nil {
			idx.log.Warn("indexer: block failed", "height", next, "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}
		last = next
		if last%100 == 0 {
			idx.log.Info("indexer progress", "height", last, "head", head)
		}
	}
}

func (idx *Indexer) indexBlock(ctx context.Context, height int64) error {
	block, err := idx.geth.GetBlockByNumber(height)
	if err != nil {
		return fmt.Errorf("get block: %w", err)
	}
	if block == nil {
		return fmt.Errorf("block %d not found", height)
	}

	// CometBFT metadata (proposer, comet block hash) — best effort
	var cometHash, proposer, appHash string
	if ci, err := idx.comet.GetBlockInfo(height); err == nil {
		cometHash = ci.Hash
		proposer = ci.Proposer
		appHash = ci.AppHash
	}

	// Collect tx hashes for batch receipt fetch
	txHashes := make([]string, len(block.Transactions))
	for i, tx := range block.Transactions {
		txHashes[i] = tx.Hash
	}

	receipts, err := idx.geth.GetReceiptsBatch(txHashes)
	if err != nil {
		return fmt.Errorf("get receipts: %w", err)
	}

	// Build a receipt map by tx hash
	receiptMap := make(map[string]*EthReceipt, len(receipts))
	for _, r := range receipts {
		if r != nil {
			receiptMap[strings.ToLower(r.TransactionHash)] = r
		}
	}

	// Begin SQLite transaction
	sqlTx, err := idx.db.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			sqlTx.Rollback()
		}
	}()

	// Insert block
	dbBlock := DBBlock{
		Height:     height,
		Hash:       strings.ToLower(block.Hash),
		ParentHash: strings.ToLower(block.ParentHash),
		CometHash:  cometHash,
		Proposer:   proposer,
		Time:       hexToInt64(block.Timestamp),
		TxCount:    len(block.Transactions),
		GasUsed:    hexToInt64(block.GasUsed),
		GasLimit:   hexToInt64(block.GasLimit),
		Size:       hexToInt64(block.Size),
		AppHash:    appHash,
		BaseFee:    hexToDec(block.BaseFeePerGas),
	}
	if _, err = sqlTx.Exec(`
INSERT OR IGNORE INTO blocks(height,hash,parent_hash,comet_hash,proposer,time,tx_count,gas_used,gas_limit,size,app_hash,base_fee)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		dbBlock.Height, dbBlock.Hash, dbBlock.ParentHash, dbBlock.CometHash, dbBlock.Proposer,
		dbBlock.Time, dbBlock.TxCount, dbBlock.GasUsed, dbBlock.GasLimit, dbBlock.Size,
		dbBlock.AppHash, dbBlock.BaseFee); err != nil {
		return fmt.Errorf("insert block: %w", err)
	}

	// Insert transactions + process receipts
	for _, tx := range block.Transactions {
		txHash := strings.ToLower(tx.Hash)
		from := strings.ToLower(tx.From)
		to := strings.ToLower(tx.To)

		gasUsed := int64(0)
		status := -1
		contractAddr := ""

		if r, ok := receiptMap[txHash]; ok {
			gasUsed = hexToInt64(r.GasUsed)
			if r.Status == "0x1" {
				status = 1
			} else if r.Status == "0x0" {
				status = 0
			}
			contractAddr = strings.ToLower(r.ContractAddress)
		}

		txType := int(hexToUint64(tx.Type))

		if _, err = sqlTx.Exec(`
INSERT OR IGNORE INTO transactions(hash,block_height,block_hash,tx_index,from_addr,to_addr,value,gas_limit,gas_used,gas_price,max_fee,max_priority_fee,nonce,input,status,contract_addr,tx_type)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			txHash, height, strings.ToLower(block.Hash),
			int(hexToUint64(tx.TransactionIndex)),
			from, to,
			hexToDec(tx.Value),
			hexToInt64(tx.Gas), gasUsed,
			hexToDec(tx.GasPrice),
			hexToDec(tx.MaxFeePerGas),
			hexToDec(tx.MaxPriorityFeePerGas),
			int64(hexToUint64(tx.Nonce)),
			tx.Input, status, contractAddr, txType); err != nil {
			return fmt.Errorf("insert tx %s: %w", txHash, err)
		}

		// Process receipt logs
		if r, ok := receiptMap[txHash]; ok {
			for _, l := range r.Logs {
				if l.Removed {
					continue
				}
				logIndex := int(hexToUint64(l.LogIndex))
				t0, t1, t2, t3 := "", "", "", ""
				if len(l.Topics) > 0 {
					t0 = l.Topics[0]
				}
				if len(l.Topics) > 1 {
					t1 = l.Topics[1]
				}
				if len(l.Topics) > 2 {
					t2 = l.Topics[2]
				}
				if len(l.Topics) > 3 {
					t3 = l.Topics[3]
				}
				logAddr := strings.ToLower(l.Address)

				if _, err = sqlTx.Exec(`
INSERT OR IGNORE INTO logs(tx_hash,block_height,log_index,address,topic0,topic1,topic2,topic3,data)
VALUES(?,?,?,?,?,?,?,?,?)`,
					txHash, height, logIndex, logAddr, t0, t1, t2, t3, l.Data); err != nil {
					return fmt.Errorf("insert log: %w", err)
				}

				// Detect ERC-20 / ERC-721 Transfer
				if t0 == transferTopic && len(l.Topics) >= 3 {
					idx.processTransfer(sqlTx, txHash, height, logIndex, logAddr, l)
				}
			}

			// Record contract creation
			if contractAddr != "" && contractAddr != "0x" {
				if _, err = sqlTx.Exec(`
INSERT OR IGNORE INTO contracts(address,creator,creation_tx,creation_block,bytecode)
VALUES(?,?,?,?,'0x')`,
					contractAddr, from, txHash, height); err != nil {
					return fmt.Errorf("insert contract: %w", err)
				}
			}
		}
	}

	if err = sqlTx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	// Update indexer state
	idx.db.SetLastIndexedBlock(height)

	// Fetch contract bytecodes in background (non-blocking)
	go idx.fetchNewContractBytecodes(height)

	return nil
}

// processTransfer handles ERC-20 and ERC-721 Transfer events within a SQL transaction.
func (idx *Indexer) processTransfer(sqlTx *sql.Tx, txHash string, height int64, logIndex int, tokenAddr string, l EthLog) {

	is721 := len(l.Topics) == 4

	// Decode addresses from topics (padded to 32 bytes, address is last 20)
	from := decodeTopicAddress(l.Topics[1])
	to := decodeTopicAddress(l.Topics[2])

	value := "0"
	tokenID := ""

	if is721 {
		// ERC-721: tokenId is topics[3]
		tokenID = hexToDec(strings.TrimPrefix(l.Topics[3], "0x"))
	} else {
		// ERC-20: value is in data field
		value = hexToDec(l.Data)
	}

	tokenType := "ERC-20"
	if is721 {
		tokenType = "ERC-721"
	}

	sqlTx.Exec(`
INSERT OR IGNORE INTO token_transfers(tx_hash,block_height,log_index,token_address,from_addr,to_addr,value,token_id,token_type)
VALUES(?,?,?,?,?,?,?,?,?)`,
		txHash, height, logIndex, tokenAddr, from, to, value, tokenID, tokenType)

	// Ensure token exists; fetch metadata if first time
	sqlTx.Exec(`
INSERT OR IGNORE INTO tokens(address,token_type,first_seen_block,name,symbol,decimals,total_supply,holder_count,transfer_count)
VALUES(?,?,?,?,?,?,?,0,0)`,
		tokenAddr, tokenType, height, "", "", 18, "0")

	// Increment transfer count
	sqlTx.Exec(`UPDATE tokens SET transfer_count=transfer_count+1 WHERE address=?`, tokenAddr)

	// Update holder balances (in-process via idx.db methods, outside the SQL tx)
	// We do this after commit to avoid nested transactions; handled in post-commit step.
	// Store pending transfer for balance update.
	idx.pendingBalanceUpdate(tokenAddr, from, to, value, is721)
}

// pendingBalances queues balance updates to apply after block commit.
// Stored as simple in-memory data, flushed after each block.
type balanceUpdate struct {
	tokenAddr string
	from, to  string
	value     string
	is721     bool
}

var pendingUpdates []balanceUpdate

func (idx *Indexer) pendingBalanceUpdate(tokenAddr, from, to, value string, is721 bool) {
	pendingUpdates = append(pendingUpdates, balanceUpdate{tokenAddr, from, to, value, is721})
}

func (idx *Indexer) flushBalanceUpdates() {
	for _, u := range pendingUpdates {
		if !u.is721 {
			idx.db.ApplyTransfer(u.tokenAddr, u.from, u.to, u.value)
		}
		// ERC-721: track as single-unit ownership
	}
	pendingUpdates = pendingUpdates[:0]
}

// fetchNewContractBytecodes fetches bytecode for newly indexed contracts.
func (idx *Indexer) fetchNewContractBytecodes(blockHeight int64) {
	rows, err := idx.db.db.Query(
		`SELECT address FROM contracts WHERE creation_block=? AND bytecode='0x'`, blockHeight)
	if err != nil {
		return
	}
	defer rows.Close()
	var addrs []string
	for rows.Next() {
		var addr string
		rows.Scan(&addr)
		addrs = append(addrs, addr)
	}
	for _, addr := range addrs {
		code, err := idx.geth.GetCode(addr)
		if err != nil || code == "0x" || code == "" {
			continue
		}
		idx.db.db.Exec(`UPDATE contracts SET bytecode=? WHERE address=?`, code, addr)

		// Fetch token metadata if we haven't already
		if !idx.db.TokenExists(addr) {
			continue
		}
		// Determine if ERC-721 by checking token_type
		var tokenType string
		idx.db.db.QueryRow(`SELECT token_type FROM tokens WHERE address=?`, addr).Scan(&tokenType)
		is721 := tokenType == "ERC-721"
		meta := idx.geth.FetchTokenMeta(addr, is721)
		if meta != nil && (meta.Name != "" || meta.Symbol != "") {
			idx.db.db.Exec(`UPDATE tokens SET name=?,symbol=?,decimals=?,total_supply=? WHERE address=? AND (name='' OR symbol='')`,
				meta.Name, meta.Symbol, meta.Decimals, meta.TotalSupply, addr)
		}
	}
}

// decodeTopicAddress extracts a 0x-prefixed lowercase address from a 32-byte topic.
func decodeTopicAddress(topic string) string {
	topic = strings.TrimPrefix(topic, "0x")
	if len(topic) < 40 {
		return "0x0000000000000000000000000000000000000000"
	}
	return "0x" + strings.ToLower(topic[len(topic)-40:])
}
