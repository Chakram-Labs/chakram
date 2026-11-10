package main

import (
	"database/sql"
	"fmt"
	"math/big"
	"strings"
)

type Database struct {
	db *sql.DB
}

func openDB(path string) (*Database, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite: single writer
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA cache_size=-32000",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("pragma: %w", err)
		}
	}
	d := &Database{db: db}
	if err := d.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return d, nil
}

func (d *Database) Close() { d.db.Close() }

func (d *Database) migrate() error {
	_, err := d.db.Exec(`
CREATE TABLE IF NOT EXISTS blocks (
    height      INTEGER PRIMARY KEY,
    hash        TEXT NOT NULL UNIQUE,
    parent_hash TEXT NOT NULL DEFAULT '',
    comet_hash  TEXT NOT NULL DEFAULT '',
    proposer    TEXT NOT NULL DEFAULT '',
    time        INTEGER NOT NULL DEFAULT 0,
    tx_count    INTEGER NOT NULL DEFAULT 0,
    gas_used    INTEGER NOT NULL DEFAULT 0,
    gas_limit   INTEGER NOT NULL DEFAULT 0,
    size        INTEGER NOT NULL DEFAULT 0,
    app_hash    TEXT NOT NULL DEFAULT '',
    base_fee    TEXT NOT NULL DEFAULT '0'
);

CREATE TABLE IF NOT EXISTS transactions (
    hash             TEXT PRIMARY KEY,
    block_height     INTEGER NOT NULL,
    block_hash       TEXT NOT NULL DEFAULT '',
    tx_index         INTEGER NOT NULL DEFAULT 0,
    from_addr        TEXT NOT NULL DEFAULT '',
    to_addr          TEXT NOT NULL DEFAULT '',
    value            TEXT NOT NULL DEFAULT '0',
    gas_limit        INTEGER NOT NULL DEFAULT 0,
    gas_used         INTEGER NOT NULL DEFAULT 0,
    gas_price        TEXT NOT NULL DEFAULT '0',
    max_fee          TEXT NOT NULL DEFAULT '0',
    max_priority_fee TEXT NOT NULL DEFAULT '0',
    nonce            INTEGER NOT NULL DEFAULT 0,
    input            TEXT NOT NULL DEFAULT '0x',
    status           INTEGER NOT NULL DEFAULT -1,
    contract_addr    TEXT NOT NULL DEFAULT '',
    tx_type          INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS logs (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    tx_hash      TEXT NOT NULL,
    block_height INTEGER NOT NULL,
    log_index    INTEGER NOT NULL,
    address      TEXT NOT NULL DEFAULT '',
    topic0       TEXT NOT NULL DEFAULT '',
    topic1       TEXT NOT NULL DEFAULT '',
    topic2       TEXT NOT NULL DEFAULT '',
    topic3       TEXT NOT NULL DEFAULT '',
    data         TEXT NOT NULL DEFAULT '0x',
    UNIQUE(tx_hash, log_index)
);

CREATE TABLE IF NOT EXISTS token_transfers (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    tx_hash       TEXT NOT NULL,
    block_height  INTEGER NOT NULL,
    log_index     INTEGER NOT NULL,
    token_address TEXT NOT NULL,
    from_addr     TEXT NOT NULL DEFAULT '',
    to_addr       TEXT NOT NULL DEFAULT '',
    value         TEXT NOT NULL DEFAULT '0',
    token_id      TEXT NOT NULL DEFAULT '',
    token_type    TEXT NOT NULL DEFAULT 'ERC-20',
    UNIQUE(tx_hash, log_index)
);

CREATE TABLE IF NOT EXISTS tokens (
    address         TEXT PRIMARY KEY,
    name            TEXT NOT NULL DEFAULT '',
    symbol          TEXT NOT NULL DEFAULT '',
    decimals        INTEGER NOT NULL DEFAULT 18,
    token_type      TEXT NOT NULL DEFAULT 'ERC-20',
    total_supply    TEXT NOT NULL DEFAULT '0',
    holder_count    INTEGER NOT NULL DEFAULT 0,
    transfer_count  INTEGER NOT NULL DEFAULT 0,
    first_seen_block INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS token_holders (
    token_address  TEXT NOT NULL,
    holder_address TEXT NOT NULL,
    balance        TEXT NOT NULL DEFAULT '0',
    PRIMARY KEY(token_address, holder_address)
);

CREATE TABLE IF NOT EXISTS contracts (
    address           TEXT PRIMARY KEY,
    creator           TEXT NOT NULL DEFAULT '',
    creation_tx       TEXT NOT NULL DEFAULT '',
    creation_block    INTEGER NOT NULL DEFAULT 0,
    bytecode          TEXT NOT NULL DEFAULT '0x',
    is_verified       INTEGER NOT NULL DEFAULT 0,
    name              TEXT NOT NULL DEFAULT '',
    source_json       TEXT NOT NULL DEFAULT '',
    abi               TEXT NOT NULL DEFAULT '',
    compiler_version  TEXT NOT NULL DEFAULT '',
    optimization      INTEGER NOT NULL DEFAULT 0,
    optimization_runs INTEGER NOT NULL DEFAULT 200,
    constructor_args  TEXT NOT NULL DEFAULT '0x',
    verified_at       INTEGER NOT NULL DEFAULT 0,
    license           TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS indexer_state (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_txs_block    ON transactions(block_height);
CREATE INDEX IF NOT EXISTS idx_txs_from     ON transactions(from_addr);
CREATE INDEX IF NOT EXISTS idx_txs_to       ON transactions(to_addr);
CREATE INDEX IF NOT EXISTS idx_txs_contract ON transactions(contract_addr) WHERE contract_addr != '';
CREATE INDEX IF NOT EXISTS idx_logs_tx      ON logs(tx_hash);
CREATE INDEX IF NOT EXISTS idx_logs_addr    ON logs(address);
CREATE INDEX IF NOT EXISTS idx_logs_topic0  ON logs(topic0);
CREATE INDEX IF NOT EXISTS idx_tt_token     ON token_transfers(token_address);
CREATE INDEX IF NOT EXISTS idx_tt_from      ON token_transfers(from_addr);
CREATE INDEX IF NOT EXISTS idx_tt_to        ON token_transfers(to_addr);
CREATE INDEX IF NOT EXISTS idx_tt_block     ON token_transfers(block_height);
CREATE INDEX IF NOT EXISTS idx_blocks_time  ON blocks(time);
	`)
	return err
}

// ── DB model types ────────────────────────────────────────────────────────────

type DBBlock struct {
	Height     int64  `json:"height"`
	Hash       string `json:"hash"`
	ParentHash string `json:"parent_hash"`
	CometHash  string `json:"comet_hash"`
	Proposer   string `json:"proposer"`
	Time       int64  `json:"time"`
	TxCount    int    `json:"tx_count"`
	GasUsed    int64  `json:"gas_used"`
	GasLimit   int64  `json:"gas_limit"`
	Size       int64  `json:"size"`
	AppHash    string `json:"app_hash"`
	BaseFee    string `json:"base_fee"`
}

type DBTx struct {
	Hash           string `json:"hash"`
	BlockHeight    int64  `json:"block_height"`
	BlockHash      string `json:"block_hash"`
	TxIndex        int    `json:"tx_index"`
	From           string `json:"from"`
	To             string `json:"to"`
	Value          string `json:"value"`
	GasLimit       int64  `json:"gas_limit"`
	GasUsed        int64  `json:"gas_used"`
	GasPrice       string `json:"gas_price"`
	MaxFee         string `json:"max_fee_per_gas"`
	MaxPriorityFee string `json:"max_priority_fee_per_gas"`
	Nonce          int64  `json:"nonce"`
	Input          string `json:"input"`
	Status         int    `json:"status"`
	ContractAddr   string `json:"contract_address"`
	TxType         int    `json:"type"`
}

type DBLog struct {
	ID          int64  `json:"id"`
	TxHash      string `json:"tx_hash"`
	BlockHeight int64  `json:"block_height"`
	LogIndex    int    `json:"log_index"`
	Address     string `json:"address"`
	Topic0      string `json:"topic0"`
	Topic1      string `json:"topic1"`
	Topic2      string `json:"topic2"`
	Topic3      string `json:"topic3"`
	Data        string `json:"data"`
}

type DBTokenTransfer struct {
	ID           int64  `json:"id"`
	TxHash       string `json:"tx_hash"`
	BlockHeight  int64  `json:"block_height"`
	LogIndex     int    `json:"log_index"`
	TokenAddress string `json:"token_address"`
	From         string `json:"from"`
	To           string `json:"to"`
	Value        string `json:"value"`
	TokenID      string `json:"token_id"`
	TokenType    string `json:"token_type"`
}

type DBToken struct {
	Address        string `json:"address"`
	Name           string `json:"name"`
	Symbol         string `json:"symbol"`
	Decimals       int    `json:"decimals"`
	TokenType      string `json:"type"`
	TotalSupply    string `json:"total_supply"`
	HolderCount    int    `json:"holder_count"`
	TransferCount  int    `json:"transfer_count"`
	FirstSeenBlock int64  `json:"first_seen_block"`
}

type DBTokenHolder struct {
	Address string `json:"address"`
	Balance string `json:"balance"`
}

type DBAddressToken struct {
	TokenAddress string `json:"token_address"`
	Name         string `json:"name"`
	Symbol       string `json:"symbol"`
	Decimals     int    `json:"decimals"`
	TokenType    string `json:"type"`
	Balance      string `json:"balance"`
}

type DBContract struct {
	Address          string `json:"address"`
	Creator          string `json:"creator"`
	CreationTx       string `json:"creation_tx"`
	CreationBlock    int64  `json:"creation_block"`
	Bytecode         string `json:"bytecode,omitempty"`
	IsVerified       bool   `json:"is_verified"`
	Name             string `json:"name"`
	SourceJSON       string `json:"source_json,omitempty"`
	ABI              string `json:"abi,omitempty"`
	CompilerVersion  string `json:"compiler_version"`
	Optimization     bool   `json:"optimization"`
	OptimizationRuns int    `json:"optimization_runs"`
	ConstructorArgs  string `json:"constructor_args"`
	VerifiedAt       int64  `json:"verified_at"`
	License          string `json:"license"`
}

type DBStats struct {
	LatestBlock    int64   `json:"latest_block"`
	TotalTxs       int64   `json:"total_txs"`
	AvgBlockTime   float64 `json:"avg_block_time_s"`
	TotalTokens    int64   `json:"total_tokens"`
	TotalContracts int64   `json:"total_contracts"`
	TotalVerified  int64   `json:"total_verified_contracts"`
	IndexedThrough int64   `json:"indexed_through"`
	IndexerStatus  string  `json:"indexer_status"`
}

// ── Indexer state ─────────────────────────────────────────────────────────────

func (d *Database) GetLastIndexedBlock() int64 {
	var v string
	d.db.QueryRow(`SELECT value FROM indexer_state WHERE key='last_block'`).Scan(&v)
	if v == "" {
		return 0
	}
	var n int64
	fmt.Sscanf(v, "%d", &n)
	return n
}

func (d *Database) SetLastIndexedBlock(height int64) {
	d.db.Exec(`INSERT INTO indexer_state(key,value) VALUES('last_block',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, fmt.Sprintf("%d", height))
}

// ── Block operations ──────────────────────────────────────────────────────────

func (d *Database) InsertBlock(b DBBlock) error {
	_, err := d.db.Exec(`
INSERT OR IGNORE INTO blocks(height,hash,parent_hash,comet_hash,proposer,time,tx_count,gas_used,gas_limit,size,app_hash,base_fee)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		b.Height, b.Hash, b.ParentHash, b.CometHash, b.Proposer,
		b.Time, b.TxCount, b.GasUsed, b.GasLimit, b.Size, b.AppHash, b.BaseFee)
	return err
}

func (d *Database) GetBlock(height int64) (*DBBlock, error) {
	return d.scanBlock(d.db.QueryRow(
		`SELECT height,hash,parent_hash,comet_hash,proposer,time,tx_count,gas_used,gas_limit,size,app_hash,base_fee FROM blocks WHERE height=?`, height))
}

func (d *Database) GetBlockByHash(hash string) (*DBBlock, error) {
	return d.scanBlock(d.db.QueryRow(
		`SELECT height,hash,parent_hash,comet_hash,proposer,time,tx_count,gas_used,gas_limit,size,app_hash,base_fee FROM blocks WHERE hash=?`, hash))
}

func (d *Database) GetLatestBlock() (*DBBlock, error) {
	return d.scanBlock(d.db.QueryRow(
		`SELECT height,hash,parent_hash,comet_hash,proposer,time,tx_count,gas_used,gas_limit,size,app_hash,base_fee FROM blocks ORDER BY height DESC LIMIT 1`))
}

func (d *Database) ListBlocks(limit, offset int) ([]DBBlock, int64, error) {
	var total int64
	d.db.QueryRow(`SELECT COUNT(*) FROM blocks`).Scan(&total)
	rows, err := d.db.Query(
		`SELECT height,hash,parent_hash,comet_hash,proposer,time,tx_count,gas_used,gas_limit,size,app_hash,base_fee FROM blocks ORDER BY height DESC LIMIT ? OFFSET ?`,
		limit, offset)
	if err != nil {
		return nil, total, err
	}
	defer rows.Close()
	var blocks []DBBlock
	for rows.Next() {
		b, err := d.scanBlockRow(rows)
		if err != nil {
			return nil, total, err
		}
		blocks = append(blocks, *b)
	}
	return blocks, total, nil
}

func (d *Database) scanBlock(row *sql.Row) (*DBBlock, error) {
	var b DBBlock
	err := row.Scan(&b.Height, &b.Hash, &b.ParentHash, &b.CometHash, &b.Proposer,
		&b.Time, &b.TxCount, &b.GasUsed, &b.GasLimit, &b.Size, &b.AppHash, &b.BaseFee)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &b, err
}

func (d *Database) scanBlockRow(rows *sql.Rows) (*DBBlock, error) {
	var b DBBlock
	err := rows.Scan(&b.Height, &b.Hash, &b.ParentHash, &b.CometHash, &b.Proposer,
		&b.Time, &b.TxCount, &b.GasUsed, &b.GasLimit, &b.Size, &b.AppHash, &b.BaseFee)
	return &b, err
}

// ── Transaction operations ────────────────────────────────────────────────────

func (d *Database) InsertTx(tx DBTx) error {
	_, err := d.db.Exec(`
INSERT OR IGNORE INTO transactions(hash,block_height,block_hash,tx_index,from_addr,to_addr,value,gas_limit,gas_used,gas_price,max_fee,max_priority_fee,nonce,input,status,contract_addr,tx_type)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		tx.Hash, tx.BlockHeight, tx.BlockHash, tx.TxIndex,
		tx.From, tx.To, tx.Value, tx.GasLimit, tx.GasUsed,
		tx.GasPrice, tx.MaxFee, tx.MaxPriorityFee,
		tx.Nonce, tx.Input, tx.Status, tx.ContractAddr, tx.TxType)
	return err
}

func (d *Database) UpdateTxReceipt(hash string, gasUsed int64, status int, contractAddr string) error {
	_, err := d.db.Exec(`UPDATE transactions SET gas_used=?,status=?,contract_addr=? WHERE hash=?`,
		gasUsed, status, contractAddr, hash)
	return err
}

func (d *Database) GetTx(hash string) (*DBTx, error) {
	row := d.db.QueryRow(`SELECT hash,block_height,block_hash,tx_index,from_addr,to_addr,value,gas_limit,gas_used,gas_price,max_fee,max_priority_fee,nonce,input,status,contract_addr,tx_type FROM transactions WHERE hash=?`, hash)
	return d.scanTxRow(row)
}

func (d *Database) ListTxs(limit, offset int) ([]DBTx, int64, error) {
	var total int64
	d.db.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&total)
	rows, err := d.db.Query(
		`SELECT hash,block_height,block_hash,tx_index,from_addr,to_addr,value,gas_limit,gas_used,gas_price,max_fee,max_priority_fee,nonce,input,status,contract_addr,tx_type FROM transactions ORDER BY block_height DESC, tx_index DESC LIMIT ? OFFSET ?`,
		limit, offset)
	if err != nil {
		return nil, total, err
	}
	defer rows.Close()
	return d.scanTxRows(rows, total)
}

func (d *Database) ListBlockTxs(height int64) ([]DBTx, error) {
	rows, err := d.db.Query(
		`SELECT hash,block_height,block_hash,tx_index,from_addr,to_addr,value,gas_limit,gas_used,gas_price,max_fee,max_priority_fee,nonce,input,status,contract_addr,tx_type FROM transactions WHERE block_height=? ORDER BY tx_index`,
		height)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	txs, _, err := d.scanTxRows(rows, 0)
	return txs, err
}

func (d *Database) ListAddressTxs(addr string, limit, offset int) ([]DBTx, int64, error) {
	addr = strings.ToLower(addr)
	var total int64
	d.db.QueryRow(`SELECT COUNT(*) FROM transactions WHERE from_addr=? OR to_addr=?`, addr, addr).Scan(&total)
	rows, err := d.db.Query(
		`SELECT hash,block_height,block_hash,tx_index,from_addr,to_addr,value,gas_limit,gas_used,gas_price,max_fee,max_priority_fee,nonce,input,status,contract_addr,tx_type FROM transactions WHERE from_addr=? OR to_addr=? ORDER BY block_height DESC, tx_index DESC LIMIT ? OFFSET ?`,
		addr, addr, limit, offset)
	if err != nil {
		return nil, total, err
	}
	defer rows.Close()
	return d.scanTxRows(rows, total)
}

func (d *Database) CountTxs() (int64, error) {
	var n int64
	err := d.db.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&n)
	return n, err
}

func (d *Database) scanTxRow(row *sql.Row) (*DBTx, error) {
	var tx DBTx
	err := row.Scan(&tx.Hash, &tx.BlockHeight, &tx.BlockHash, &tx.TxIndex,
		&tx.From, &tx.To, &tx.Value, &tx.GasLimit, &tx.GasUsed,
		&tx.GasPrice, &tx.MaxFee, &tx.MaxPriorityFee, &tx.Nonce,
		&tx.Input, &tx.Status, &tx.ContractAddr, &tx.TxType)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &tx, err
}

func (d *Database) scanTxRows(rows *sql.Rows, total int64) ([]DBTx, int64, error) {
	var txs []DBTx
	for rows.Next() {
		var tx DBTx
		if err := rows.Scan(&tx.Hash, &tx.BlockHeight, &tx.BlockHash, &tx.TxIndex,
			&tx.From, &tx.To, &tx.Value, &tx.GasLimit, &tx.GasUsed,
			&tx.GasPrice, &tx.MaxFee, &tx.MaxPriorityFee, &tx.Nonce,
			&tx.Input, &tx.Status, &tx.ContractAddr, &tx.TxType); err != nil {
			return nil, total, err
		}
		txs = append(txs, tx)
	}
	return txs, total, nil
}

// ── Log operations ────────────────────────────────────────────────────────────

func (d *Database) InsertLog(l DBLog) error {
	_, err := d.db.Exec(`
INSERT OR IGNORE INTO logs(tx_hash,block_height,log_index,address,topic0,topic1,topic2,topic3,data)
VALUES(?,?,?,?,?,?,?,?,?)`,
		l.TxHash, l.BlockHeight, l.LogIndex, l.Address,
		l.Topic0, l.Topic1, l.Topic2, l.Topic3, l.Data)
	return err
}

func (d *Database) GetTxLogs(txHash string) ([]DBLog, error) {
	rows, err := d.db.Query(
		`SELECT id,tx_hash,block_height,log_index,address,topic0,topic1,topic2,topic3,data FROM logs WHERE tx_hash=? ORDER BY log_index`,
		txHash)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return d.scanLogRows(rows)
}

func (d *Database) scanLogRows(rows *sql.Rows) ([]DBLog, error) {
	var logs []DBLog
	for rows.Next() {
		var l DBLog
		if err := rows.Scan(&l.ID, &l.TxHash, &l.BlockHeight, &l.LogIndex,
			&l.Address, &l.Topic0, &l.Topic1, &l.Topic2, &l.Topic3, &l.Data); err != nil {
			return nil, err
		}
		logs = append(logs, l)
	}
	return logs, nil
}

// ── Token transfer operations ─────────────────────────────────────────────────

func (d *Database) InsertTokenTransfer(tt DBTokenTransfer) error {
	_, err := d.db.Exec(`
INSERT OR IGNORE INTO token_transfers(tx_hash,block_height,log_index,token_address,from_addr,to_addr,value,token_id,token_type)
VALUES(?,?,?,?,?,?,?,?,?)`,
		tt.TxHash, tt.BlockHeight, tt.LogIndex, tt.TokenAddress,
		tt.From, tt.To, tt.Value, tt.TokenID, tt.TokenType)
	return err
}

func (d *Database) ListTokenTransfers(tokenAddr string, limit, offset int) ([]DBTokenTransfer, int64, error) {
	tokenAddr = strings.ToLower(tokenAddr)
	var total int64
	d.db.QueryRow(`SELECT COUNT(*) FROM token_transfers WHERE token_address=?`, tokenAddr).Scan(&total)
	rows, err := d.db.Query(
		`SELECT id,tx_hash,block_height,log_index,token_address,from_addr,to_addr,value,token_id,token_type FROM token_transfers WHERE token_address=? ORDER BY block_height DESC, log_index DESC LIMIT ? OFFSET ?`,
		tokenAddr, limit, offset)
	if err != nil {
		return nil, total, err
	}
	defer rows.Close()
	return d.scanTTRows(rows, total)
}

func (d *Database) ListAddressTokenTransfers(addr string, limit, offset int) ([]DBTokenTransfer, int64, error) {
	addr = strings.ToLower(addr)
	var total int64
	d.db.QueryRow(`SELECT COUNT(*) FROM token_transfers WHERE from_addr=? OR to_addr=?`, addr, addr).Scan(&total)
	rows, err := d.db.Query(
		`SELECT id,tx_hash,block_height,log_index,token_address,from_addr,to_addr,value,token_id,token_type FROM token_transfers WHERE from_addr=? OR to_addr=? ORDER BY block_height DESC, log_index DESC LIMIT ? OFFSET ?`,
		addr, addr, limit, offset)
	if err != nil {
		return nil, total, err
	}
	defer rows.Close()
	return d.scanTTRows(rows, total)
}

func (d *Database) scanTTRows(rows *sql.Rows, total int64) ([]DBTokenTransfer, int64, error) {
	var tts []DBTokenTransfer
	for rows.Next() {
		var tt DBTokenTransfer
		if err := rows.Scan(&tt.ID, &tt.TxHash, &tt.BlockHeight, &tt.LogIndex,
			&tt.TokenAddress, &tt.From, &tt.To, &tt.Value, &tt.TokenID, &tt.TokenType); err != nil {
			return nil, total, err
		}
		tts = append(tts, tt)
	}
	return tts, total, nil
}

// ── Token operations ──────────────────────────────────────────────────────────

func (d *Database) UpsertToken(t DBToken) error {
	_, err := d.db.Exec(`
INSERT INTO tokens(address,name,symbol,decimals,token_type,total_supply,holder_count,transfer_count,first_seen_block)
VALUES(?,?,?,?,?,?,?,?,?)
ON CONFLICT(address) DO UPDATE SET
    name=CASE WHEN excluded.name!='' THEN excluded.name ELSE name END,
    symbol=CASE WHEN excluded.symbol!='' THEN excluded.symbol ELSE symbol END,
    decimals=excluded.decimals,
    total_supply=CASE WHEN excluded.total_supply!='0' THEN excluded.total_supply ELSE total_supply END`,
		t.Address, t.Name, t.Symbol, t.Decimals, t.TokenType,
		t.TotalSupply, t.HolderCount, t.TransferCount, t.FirstSeenBlock)
	return err
}

func (d *Database) GetToken(addr string) (*DBToken, error) {
	addr = strings.ToLower(addr)
	row := d.db.QueryRow(
		`SELECT address,name,symbol,decimals,token_type,total_supply,holder_count,transfer_count,first_seen_block FROM tokens WHERE address=?`, addr)
	var t DBToken
	err := row.Scan(&t.Address, &t.Name, &t.Symbol, &t.Decimals, &t.TokenType,
		&t.TotalSupply, &t.HolderCount, &t.TransferCount, &t.FirstSeenBlock)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &t, err
}

func (d *Database) TokenExists(addr string) bool {
	var count int
	d.db.QueryRow(`SELECT COUNT(*) FROM tokens WHERE address=?`, strings.ToLower(addr)).Scan(&count)
	return count > 0
}

func (d *Database) ListTokens(limit, offset int) ([]DBToken, int64, error) {
	var total int64
	d.db.QueryRow(`SELECT COUNT(*) FROM tokens`).Scan(&total)
	rows, err := d.db.Query(
		`SELECT address,name,symbol,decimals,token_type,total_supply,holder_count,transfer_count,first_seen_block FROM tokens ORDER BY transfer_count DESC LIMIT ? OFFSET ?`,
		limit, offset)
	if err != nil {
		return nil, total, err
	}
	defer rows.Close()
	var tokens []DBToken
	for rows.Next() {
		var t DBToken
		if err := rows.Scan(&t.Address, &t.Name, &t.Symbol, &t.Decimals, &t.TokenType,
			&t.TotalSupply, &t.HolderCount, &t.TransferCount, &t.FirstSeenBlock); err != nil {
			return nil, total, err
		}
		tokens = append(tokens, t)
	}
	return tokens, total, nil
}

func (d *Database) IncrTokenTransfer(addr string) {
	d.db.Exec(`UPDATE tokens SET transfer_count=transfer_count+1 WHERE address=?`, strings.ToLower(addr))
}

// ── Token holder operations ───────────────────────────────────────────────────

func (d *Database) GetHolderBalance(tokenAddr, holderAddr string) string {
	var bal string
	d.db.QueryRow(`SELECT balance FROM token_holders WHERE token_address=? AND holder_address=?`,
		strings.ToLower(tokenAddr), strings.ToLower(holderAddr)).Scan(&bal)
	if bal == "" {
		return "0"
	}
	return bal
}

func (d *Database) SetHolderBalance(tokenAddr, holderAddr, balance string) {
	tokenAddr = strings.ToLower(tokenAddr)
	holderAddr = strings.ToLower(holderAddr)
	if balance == "0" || balance == "" {
		d.db.Exec(`DELETE FROM token_holders WHERE token_address=? AND holder_address=?`, tokenAddr, holderAddr)
		d.db.Exec(`UPDATE tokens SET holder_count=(SELECT COUNT(*) FROM token_holders WHERE token_address=?) WHERE address=?`, tokenAddr, tokenAddr)
		return
	}
	d.db.Exec(`
INSERT INTO token_holders(token_address,holder_address,balance) VALUES(?,?,?)
ON CONFLICT(token_address,holder_address) DO UPDATE SET balance=excluded.balance`,
		tokenAddr, holderAddr, balance)
	d.db.Exec(`UPDATE tokens SET holder_count=(SELECT COUNT(*) FROM token_holders WHERE token_address=?) WHERE address=?`, tokenAddr, tokenAddr)
}

func (d *Database) ApplyTransfer(tokenAddr, from, to, amount string) {
	if from != "0x0000000000000000000000000000000000000000" {
		old := d.GetHolderBalance(tokenAddr, from)
		d.SetHolderBalance(tokenAddr, from, subBigStr(old, amount))
	}
	if to != "0x0000000000000000000000000000000000000000" {
		old := d.GetHolderBalance(tokenAddr, to)
		d.SetHolderBalance(tokenAddr, to, addBigStr(old, amount))
	}
}

func (d *Database) ListTokenHolders(tokenAddr string, limit, offset int) ([]DBTokenHolder, int64, error) {
	tokenAddr = strings.ToLower(tokenAddr)
	var total int64
	d.db.QueryRow(`SELECT COUNT(*) FROM token_holders WHERE token_address=? AND balance!='0'`, tokenAddr).Scan(&total)
	rows, err := d.db.Query(
		`SELECT holder_address, balance FROM token_holders WHERE token_address=? AND balance!='0' ORDER BY length(balance) DESC, balance DESC LIMIT ? OFFSET ?`,
		tokenAddr, limit, offset)
	if err != nil {
		return nil, total, err
	}
	defer rows.Close()
	var holders []DBTokenHolder
	for rows.Next() {
		var h DBTokenHolder
		if err := rows.Scan(&h.Address, &h.Balance); err != nil {
			return nil, total, err
		}
		holders = append(holders, h)
	}
	return holders, total, nil
}

func (d *Database) ListAddressTokens(holderAddr string) ([]DBAddressToken, error) {
	rows, err := d.db.Query(`
SELECT th.token_address, t.name, t.symbol, t.decimals, t.token_type, th.balance
FROM token_holders th
JOIN tokens t ON t.address=th.token_address
WHERE th.holder_address=? AND th.balance!='0'
ORDER BY length(th.balance) DESC, th.balance DESC`,
		strings.ToLower(holderAddr))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tokens []DBAddressToken
	for rows.Next() {
		var t DBAddressToken
		if err := rows.Scan(&t.TokenAddress, &t.Name, &t.Symbol, &t.Decimals, &t.TokenType, &t.Balance); err != nil {
			return nil, err
		}
		tokens = append(tokens, t)
	}
	return tokens, nil
}

// ── Contract operations ───────────────────────────────────────────────────────

func (d *Database) InsertContract(c DBContract) error {
	_, err := d.db.Exec(`
INSERT OR IGNORE INTO contracts(address,creator,creation_tx,creation_block,bytecode)
VALUES(?,?,?,?,?)`,
		strings.ToLower(c.Address), c.Creator, c.CreationTx, c.CreationBlock, c.Bytecode)
	return err
}

func (d *Database) GetContract(addr string) (*DBContract, error) {
	addr = strings.ToLower(addr)
	row := d.db.QueryRow(`
SELECT address,creator,creation_tx,creation_block,bytecode,is_verified,name,source_json,abi,compiler_version,optimization,optimization_runs,constructor_args,verified_at,license
FROM contracts WHERE address=?`, addr)
	var c DBContract
	var isVerified, optEnabled int
	err := row.Scan(&c.Address, &c.Creator, &c.CreationTx, &c.CreationBlock, &c.Bytecode,
		&isVerified, &c.Name, &c.SourceJSON, &c.ABI, &c.CompilerVersion,
		&optEnabled, &c.OptimizationRuns, &c.ConstructorArgs, &c.VerifiedAt, &c.License)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	c.IsVerified = isVerified == 1
	c.Optimization = optEnabled == 1
	return &c, err
}

func (d *Database) ContractExists(addr string) bool {
	var n int
	d.db.QueryRow(`SELECT COUNT(*) FROM contracts WHERE address=?`, strings.ToLower(addr)).Scan(&n)
	return n > 0
}

func (d *Database) UpdateVerification(addr string, name, sourceJSON, abi, compVer, constructorArgs, license string, optimization bool, optimizationRuns int, verifiedAt int64) error {
	opt := 0
	if optimization {
		opt = 1
	}
	_, err := d.db.Exec(`
UPDATE contracts SET is_verified=1,name=?,source_json=?,abi=?,compiler_version=?,optimization=?,optimization_runs=?,constructor_args=?,verified_at=?,license=?
WHERE address=?`,
		name, sourceJSON, abi, compVer, opt, optimizationRuns, constructorArgs, verifiedAt,
		license, strings.ToLower(addr))
	return err
}

// ── Stats ─────────────────────────────────────────────────────────────────────

func (d *Database) GetStats() DBStats {
	var s DBStats
	d.db.QueryRow(`SELECT COUNT(*) FROM blocks`).Scan(&s.LatestBlock)
	// Actual latest height
	var latest int64
	d.db.QueryRow(`SELECT COALESCE(MAX(height),0) FROM blocks`).Scan(&latest)
	s.LatestBlock = latest

	d.db.QueryRow(`SELECT COUNT(*) FROM transactions`).Scan(&s.TotalTxs)
	d.db.QueryRow(`SELECT COUNT(*) FROM tokens`).Scan(&s.TotalTokens)
	d.db.QueryRow(`SELECT COUNT(*) FROM contracts`).Scan(&s.TotalContracts)
	d.db.QueryRow(`SELECT COUNT(*) FROM contracts WHERE is_verified=1`).Scan(&s.TotalVerified)
	s.IndexedThrough = d.GetLastIndexedBlock()

	// Avg block time from last 100 blocks
	rows, err := d.db.Query(`SELECT time FROM blocks ORDER BY height DESC LIMIT 100`)
	if err == nil {
		defer rows.Close()
		var times []int64
		for rows.Next() {
			var t int64
			rows.Scan(&t)
			times = append(times, t)
		}
		if len(times) >= 2 {
			span := times[0] - times[len(times)-1]
			if span > 0 {
				s.AvgBlockTime = float64(span) / float64(len(times)-1)
			}
		}
	}

	if s.IndexedThrough >= s.LatestBlock {
		s.IndexerStatus = "synced"
	} else {
		s.IndexerStatus = "syncing"
	}
	return s
}

// ── Search helpers ────────────────────────────────────────────────────────────

func (d *Database) BlockExists(height int64) bool {
	var n int
	d.db.QueryRow(`SELECT COUNT(*) FROM blocks WHERE height=?`, height).Scan(&n)
	return n > 0
}

func (d *Database) TxExists(hash string) bool {
	var n int
	d.db.QueryRow(`SELECT COUNT(*) FROM transactions WHERE hash=?`, strings.ToLower(hash)).Scan(&n)
	return n > 0
}

// bigStr returns "0" for empty/nil big.Int strings
func bigStr(s string) string {
	if s == "" {
		return "0"
	}
	n := new(big.Int)
	if _, ok := n.SetString(s, 10); !ok {
		return "0"
	}
	return n.String()
}
