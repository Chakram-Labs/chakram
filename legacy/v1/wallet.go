// wallet.go — Complete wallet system for Chakram.
// Handles key generation, addresses, BIP39 mnemonics, signing, and encrypted wallet files.
package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/ripemd160"
)

// ── Base58 ────────────────────────────────────────────────────────────────────

const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

func base58Encode(input []byte) string {
	leadingZeros := 0
	for _, b := range input {
		if b == 0 {
			leadingZeros++
		} else {
			break
		}
	}

	n := new(big.Int).SetBytes(input)
	base := big.NewInt(58)
	mod := new(big.Int)

	var encoded []byte
	for n.Sign() > 0 {
		n.DivMod(n, base, mod)
		encoded = append(encoded, base58Alphabet[mod.Int64()])
	}
	for i := 0; i < leadingZeros; i++ {
		encoded = append(encoded, base58Alphabet[0])
	}
	// reverse
	for i, j := 0, len(encoded)-1; i < j; i, j = i+1, j-1 {
		encoded[i], encoded[j] = encoded[j], encoded[i]
	}
	return string(encoded)
}

func base58Decode(s string) ([]byte, error) {
	n := big.NewInt(0)
	base := big.NewInt(58)

	for _, c := range s {
		idx := strings.IndexRune(base58Alphabet, c)
		if idx < 0 {
			return nil, fmt.Errorf("invalid base58 character: %c", c)
		}
		n.Mul(n, base)
		n.Add(n, big.NewInt(int64(idx)))
	}

	decoded := n.Bytes()

	leadingOnes := 0
	for _, c := range s {
		if c == '1' {
			leadingOnes++
		} else {
			break
		}
	}

	result := make([]byte, leadingOnes+len(decoded))
	copy(result[leadingOnes:], decoded)
	return result, nil
}

// ── Types ─────────────────────────────────────────────────────────────────────

// KeyPair holds an Ed25519 private/public key pair.
type KeyPair struct {
	PrivateKey ed25519.PrivateKey // 64 bytes
	PublicKey  ed25519.PublicKey  // 32 bytes
}

// Wallet is a fully derived Chakram wallet: keys, address, and mnemonic.
type Wallet struct {
	KeyPair  KeyPair
	Address  string // CK1... Base58Check address
	Mnemonic string // 12-word BIP39 seed phrase
}

// ── Key and address functions ─────────────────────────────────────────────────

// GenerateKeyPair generates a fresh Ed25519 key pair using crypto/rand.
func GenerateKeyPair() (KeyPair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return KeyPair{}, err
	}
	return KeyPair{PrivateKey: priv, PublicKey: pub}, nil
}

// PubKeyToHash returns RIPEMD160(SHA256(pubKey)) — the 20-byte address hash
// used in UTXOs and P2PKH scripts. This is the exported canonical implementation;
// utxo.go delegates to this function.
func PubKeyToHash(pubKey []byte) []byte {
	sha := sha256.Sum256(pubKey)
	r := ripemd160.New()
	r.Write(sha[:])
	return r.Sum(nil)
}

// addrPrefix is the human-readable prefix prepended to every Chakram address.
const addrPrefix = "CK1"

// PubKeyHashToAddress encodes a 20-byte public key hash into a CK1... address.
//
// Derivation:
//  1. checksum = SHA256(SHA256(pubKeyHash))[:4]
//  2. payload  = pubKeyHash ‖ checksum        (24 bytes)
//  3. address  = "CK1" + Base58(payload)
//
// The "CK1" prefix is hardcoded so every address consistently starts with it,
// regardless of the hash value. (Standard Base58Check cannot guarantee a fixed
// multi-character prefix because the 2nd+ characters depend on the hash.)
func PubKeyHashToAddress(pubKeyHash []byte) string {
	first := sha256.Sum256(pubKeyHash)
	second := sha256.Sum256(first[:])
	checksum := second[:4]
	payload := append(pubKeyHash, checksum...)
	return addrPrefix + base58Encode(payload)
}

// AddressToPubKeyHash decodes a CK1... address back to its 20-byte public key hash.
// Returns an error if the checksum is invalid or the address is malformed.
func AddressToPubKeyHash(address string) ([]byte, error) {
	if !strings.HasPrefix(address, addrPrefix) {
		return nil, fmt.Errorf("invalid address: must start with %s", addrPrefix)
	}

	decoded, err := base58Decode(address[len(addrPrefix):])
	if err != nil {
		return nil, fmt.Errorf("invalid address encoding: %w", err)
	}
	if len(decoded) != 24 {
		return nil, fmt.Errorf("invalid address length: expected 24 bytes, got %d", len(decoded))
	}

	pubKeyHash := decoded[:20]
	checksum := decoded[20:]

	first := sha256.Sum256(pubKeyHash)
	second := sha256.Sum256(first[:])
	expected := second[:4]

	if !bytes.Equal(checksum, expected) {
		return nil, errors.New("invalid address: checksum mismatch")
	}
	return pubKeyHash, nil
}

// ValidateAddress reports whether address is a valid Chakram address.
func ValidateAddress(address string) bool {
	_, err := AddressToPubKeyHash(address)
	return err == nil
}

// ── Mnemonic ──────────────────────────────────────────────────────────────────

// entropyToMnemonic converts 16 bytes of entropy into a 12-word BIP39 mnemonic.
// entropy (128 bits) + 4 checksum bits = 132 bits → 12 × 11-bit indices.
func entropyToMnemonic(entropy []byte) (string, error) {
	if len(bip39Words) != 2048 {
		return "", errors.New("BIP39 wordlist not loaded (expected 2048 words)")
	}

	h := sha256.Sum256(entropy)
	cs := h[0] >> 4 // top 4 bits

	// Build 132-bit number: 128 bits of entropy + 4 checksum bits.
	n := new(big.Int).SetBytes(entropy)
	n.Lsh(n, 4)
	n.Or(n, big.NewInt(int64(cs)))

	mask := big.NewInt(0x7FF) // 11 bits
	words := make([]string, 12)
	for i := 11; i >= 0; i-- {
		idx := new(big.Int).And(n, mask).Int64()
		words[i] = bip39Words[idx]
		n.Rsh(n, 11)
	}
	return strings.Join(words, " "), nil
}

// mnemonicToEntropy reverses entropyToMnemonic to recover the 16-byte entropy.
func mnemonicToEntropy(mnemonic string) ([]byte, error) {
	if len(bip39Words) != 2048 {
		return nil, errors.New("BIP39 wordlist not loaded (expected 2048 words)")
	}

	wordIndex := make(map[string]int, 2048)
	for i, w := range bip39Words {
		wordIndex[w] = i
	}

	parts := strings.Fields(mnemonic)
	if len(parts) != 12 {
		return nil, fmt.Errorf("mnemonic must be 12 words, got %d", len(parts))
	}

	// Reconstruct the 132-bit number.
	n := big.NewInt(0)
	for _, word := range parts {
		idx, ok := wordIndex[word]
		if !ok {
			return nil, fmt.Errorf("unknown mnemonic word: %q", word)
		}
		n.Lsh(n, 11)
		n.Or(n, big.NewInt(int64(idx)))
	}

	// Extract 128-bit entropy (top 128 of 132 bits, i.e. shift right by 4).
	entropy := new(big.Int).Rsh(n, 4)
	raw := entropy.Bytes()

	// Pad to exactly 16 bytes.
	padded := make([]byte, 16)
	copy(padded[16-len(raw):], raw)

	// Verify checksum (bottom 4 bits of n).
	csGot := new(big.Int).And(n, big.NewInt(0xF)).Int64()
	h := sha256.Sum256(padded)
	csExpected := int64(h[0] >> 4)
	if csGot != csExpected {
		return nil, errors.New("mnemonic checksum mismatch — invalid mnemonic")
	}
	return padded, nil
}

// ── Wallet construction ───────────────────────────────────────────────────────

// NewWallet generates a fresh Chakram wallet with keys, address, and mnemonic.
// The key is derived deterministically from 16 bytes of random entropy so that
// WalletFromMnemonic can reconstruct the exact same key and address.
func NewWallet() (*Wallet, error) {
	entropy := make([]byte, 16)
	if _, err := rand.Read(entropy); err != nil {
		return nil, fmt.Errorf("generate entropy: %w", err)
	}

	seed := sha256.Sum256(entropy) // 32-byte Ed25519 seed
	priv := ed25519.NewKeyFromSeed(seed[:])
	pub := priv.Public().(ed25519.PublicKey)

	kp := KeyPair{PrivateKey: priv, PublicKey: pub}
	pkh := PubKeyToHash(pub)
	address := PubKeyHashToAddress(pkh)

	mnemonic, err := entropyToMnemonic(entropy)
	if err != nil {
		return nil, fmt.Errorf("generate mnemonic: %w", err)
	}

	return &Wallet{KeyPair: kp, Address: address, Mnemonic: mnemonic}, nil
}

// WalletFromMnemonic restores a Chakram wallet from a 12-word BIP39 mnemonic.
// The private key is derived deterministically as ed25519.NewKeyFromSeed(SHA256(entropy)).
func WalletFromMnemonic(mnemonic string) (*Wallet, error) {
	entropy, err := mnemonicToEntropy(mnemonic)
	if err != nil {
		return nil, fmt.Errorf("decode mnemonic: %w", err)
	}

	seed := sha256.Sum256(entropy) // 32-byte Ed25519 seed
	priv := ed25519.NewKeyFromSeed(seed[:])
	pub := priv.Public().(ed25519.PublicKey)

	pkh := PubKeyToHash(pub)
	address := PubKeyHashToAddress(pkh)

	return &Wallet{
		KeyPair:  KeyPair{PrivateKey: priv, PublicKey: pub},
		Address:  address,
		Mnemonic: mnemonic,
	}, nil
}

// ── Wallet methods ────────────────────────────────────────────────────────────

// Sign signs message with the wallet's Ed25519 private key.
func (w *Wallet) Sign(message []byte) []byte {
	return ed25519.Sign(w.KeyPair.PrivateKey, message)
}

// GetPubKeyHash returns the 20-byte public key hash for this wallet's address.
func (w *Wallet) GetPubKeyHash() []byte {
	return PubKeyToHash(w.KeyPair.PublicKey)
}

// GetBalance returns the total unspent balance (in Cash) for this wallet.
func (w *Wallet) GetBalance(utxoSet *UTXOSet) (uint64, error) {
	return utxoSet.GetBalance(w.GetPubKeyHash())
}

// ── File persistence ──────────────────────────────────────────────────────────

type walletFile struct {
	Address             string `json:"address"`
	Mnemonic            string `json:"mnemonic"`
	PublicKey           string `json:"public_key"`
	KDFSalt             string `json:"kdf_salt,omitempty"` // hex-encoded 16-byte Argon2id salt; absent in legacy files
	Nonce               string `json:"nonce"`
	EncryptedPrivateKey string `json:"encrypted_private_key"`
}

// deriveAESKey derives a 32-byte AES key from password.
// When salt is non-empty it uses Argon2id (secure); when salt is empty it falls
// back to raw SHA256 for backward compatibility with legacy wallet files.
func deriveAESKey(password string, salt []byte) []byte {
	if len(salt) == 0 {
		h := sha256.Sum256([]byte(password))
		return h[:]
	}
	// Argon2id: time=3, memory=64 MB, threads=4, key=32 bytes.
	return argon2.IDKey([]byte(password), salt, 3, 64*1024, 4, 32)
}

// SaveToFile encrypts and saves the wallet to path using AES-256-GCM.
// The AES key is derived via Argon2id with a fresh random 16-byte salt.
func (w *Wallet) SaveToFile(path string, password string) error {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("generate salt: %w", err)
	}

	aesKey := deriveAESKey(password, salt)
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("create GCM: %w", err)
	}

	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("generate nonce: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, w.KeyPair.PrivateKey, nil)

	wf := walletFile{
		Address:             w.Address,
		Mnemonic:            w.Mnemonic,
		PublicKey:           hex.EncodeToString(w.KeyPair.PublicKey),
		KDFSalt:             hex.EncodeToString(salt),
		Nonce:               hex.EncodeToString(nonce),
		EncryptedPrivateKey: hex.EncodeToString(ciphertext),
	}

	data, err := json.MarshalIndent(wf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal wallet: %w", err)
	}
	return os.WriteFile(path, data, 0600)
}

// LoadWalletFromFile decrypts and restores a wallet from path.
// Supports Argon2id-derived keys (new format with kdf_salt) and legacy SHA256
// keys (old format without kdf_salt) for backward compatibility.
func LoadWalletFromFile(path string, password string) (*Wallet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read wallet file: %w", err)
	}

	var wf walletFile
	if err := json.Unmarshal(data, &wf); err != nil {
		return nil, fmt.Errorf("parse wallet file: %w", err)
	}

	var salt []byte
	if wf.KDFSalt != "" {
		salt, err = hex.DecodeString(wf.KDFSalt)
		if err != nil {
			return nil, fmt.Errorf("decode kdf_salt: %w", err)
		}
	}

	aesKey := deriveAESKey(password, salt) // salt=nil → legacy SHA256 path
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}

	nonce, err := hex.DecodeString(wf.Nonce)
	if err != nil {
		return nil, fmt.Errorf("decode nonce: %w", err)
	}
	ciphertext, err := hex.DecodeString(wf.EncryptedPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("decode ciphertext: %w", err)
	}

	privBytes, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, errors.New("decrypt failed: wrong password or corrupted file")
	}

	priv := ed25519.PrivateKey(privBytes)
	pub := priv.Public().(ed25519.PublicKey)

	pkh := PubKeyToHash(pub)
	address := PubKeyHashToAddress(pkh)

	return &Wallet{
		KeyPair:  KeyPair{PrivateKey: priv, PublicKey: pub},
		Address:  address,
		Mnemonic: wf.Mnemonic,
	}, nil
}

// ── Transaction signing ───────────────────────────────────────────────────────

// SignTransaction fills the Signature and PublicKey fields of every input in tx.
// chainID is committed to in every sighash to prevent cross-network replay.
// Returns an error if tx is a coinbase transaction (coinbase has no inputs to sign).
func SignTransaction(tx *Transaction, wallet *Wallet, chainID []byte) error {
	if tx.IsCoinbase {
		return errors.New("cannot sign coinbase transaction")
	}
	for i := range tx.Inputs {
		in := &tx.Inputs[i]
		msg := signedMessage(chainID, tx, i)
		in.Signature = wallet.Sign(msg)
		in.PublicKey = wallet.KeyPair.PublicKey
	}
	return nil
}
