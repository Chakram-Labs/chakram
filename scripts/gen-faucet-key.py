#!/usr/bin/env python3
"""
Generate a new secp256k1 key for the testnet faucet.

Prints only the checksummed Ethereum address to stdout.
Saves the raw 32-byte private key as hex to ~/.chakram/testnet-faucet.key
with 0600 permissions.

Usage:
    python3 scripts/gen-faucet-key.py
"""

import os
import sys
import stat

from cryptography.hazmat.primitives.asymmetric.ec import generate_private_key, SECP256K1
from cryptography.hazmat.backends import default_backend
from cryptography.hazmat.primitives.serialization import Encoding, PublicFormat

try:
    from Crypto.Hash import keccak as _pycrypto_keccak
    def keccak256(data: bytes) -> bytes:
        k = _pycrypto_keccak.new(digest_bits=256)
        k.update(data)
        return k.digest()
except ImportError:
    # Fallback: pysha3 / sha3 via hashlib on newer Python
    import hashlib
    def keccak256(data: bytes) -> bytes:
        return hashlib.new("sha3_256", data).digest()  # NOTE: sha3_256 != keccak256
        # If this fallback is reached, install pycryptodome: pip3 install pycryptodome


def eip55_checksum(addr_hex: str) -> str:
    """Return EIP-55 checksummed Ethereum address."""
    addr = addr_hex.lower()
    digest = keccak256(addr.encode())
    out = "0x"
    for i, c in enumerate(addr):
        if c in "0123456789":
            out += c
        elif (digest[i >> 1] >> (4 if (i % 2 == 0) else 0)) & 0x08:
            out += c.upper()
        else:
            out += c.lower()
    return out


def main():
    out_dir = os.path.expanduser("~/.chakram")
    out_path = os.path.join(out_dir, "testnet-faucet.key")

    if os.path.exists(out_path):
        print(f"error: {out_path} already exists — refusing to overwrite.", file=sys.stderr)
        print("Delete it manually if you want to regenerate the faucet key.", file=sys.stderr)
        sys.exit(1)

    # Generate secp256k1 key.
    priv = generate_private_key(SECP256K1(), default_backend())

    # Raw 32-byte private key.
    priv_int = priv.private_numbers().private_value
    priv_hex = f"{priv_int:064x}"

    # Uncompressed public key: 04 || X (32 bytes) || Y (32 bytes) → drop the 04 prefix.
    pub_bytes = priv.public_key().public_bytes(Encoding.X962, PublicFormat.UncompressedPoint)
    assert len(pub_bytes) == 65 and pub_bytes[0] == 0x04
    pub_raw = pub_bytes[1:]  # 64 bytes: X || Y

    # Ethereum address = last 20 bytes of keccak256(pub_raw).
    addr_bytes = keccak256(pub_raw)[-20:]
    addr_hex = addr_bytes.hex()
    addr = eip55_checksum(addr_hex)

    # Write private key file with 0600 permissions.
    os.makedirs(out_dir, mode=0o700, exist_ok=True)
    fd = os.open(out_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    try:
        os.write(fd, (priv_hex + "\n").encode())
    finally:
        os.close(fd)

    # Verify permissions are exactly 0600.
    mode = stat.S_IMODE(os.stat(out_path).st_mode)
    if mode != 0o600:
        os.chmod(out_path, 0o600)

    print(addr)
    print(f"private key saved to {out_path} (permissions: {oct(mode)})", file=sys.stderr)


if __name__ == "__main__":
    main()
