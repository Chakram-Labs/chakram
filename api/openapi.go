package api

// openAPISpec returns the embedded OpenAPI 3.0 JSON spec.
func openAPISpec() []byte {
	return []byte(`{
  "openapi": "3.0.3",
  "info": {
    "title": "Chakram Node API",
    "version": "1.0.0",
    "description": "REST API for the Chakram blockchain node (CometBFT + geth Engine API)."
  },
  "paths": {
    "/api/status": {
      "get": {
        "summary": "Node status",
        "responses": {
          "200": {"description": "OK", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/StatusResponse"}}}}
        }
      }
    },
    "/api/validator": {
      "get": {
        "summary": "Validator info",
        "responses": {
          "200": {"description": "OK", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ValidatorResponse"}}}}
        }
      }
    },
    "/api/blocks": {
      "get": {
        "summary": "List recent blocks",
        "parameters": [
          {"name": "limit", "in": "query", "schema": {"type": "integer", "default": 20, "maximum": 100}},
          {"name": "offset", "in": "query", "schema": {"type": "integer", "default": 0}}
        ],
        "responses": {
          "200": {"description": "OK", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/BlocksResponse"}}}}
        }
      }
    },
    "/api/block/{height}": {
      "get": {
        "summary": "Block by height",
        "parameters": [{"name": "height", "in": "path", "required": true, "schema": {"type": "integer"}}],
        "responses": {
          "200": {"description": "OK", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/BlockResponse"}}}},
          "404": {"description": "Not found"}
        }
      }
    },
    "/api/tx/{hash}": {
      "get": {
        "summary": "Transaction by hash",
        "parameters": [{"name": "hash", "in": "path", "required": true, "schema": {"type": "string"}}],
        "responses": {
          "200": {"description": "OK", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/TxResponse"}}}},
          "404": {"description": "Not found"}
        }
      }
    },
    "/api/address/{addr}": {
      "get": {
        "summary": "Address balance and nonce",
        "parameters": [{"name": "addr", "in": "path", "required": true, "schema": {"type": "string"}}],
        "responses": {
          "200": {"description": "OK", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/AddressResponse"}}}}
        }
      }
    },
    "/api/peers": {
      "get": {
        "summary": "Connected peers",
        "responses": {
          "200": {"description": "OK", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/PeersResponse"}}}}
        }
      }
    },
    "/api/logs": {
      "get": {
        "summary": "Node log stream (Server-Sent Events)",
        "responses": {
          "200": {"description": "SSE stream", "content": {"text/event-stream": {}}}
        }
      }
    },
    "/api/faucet": {
      "post": {
        "summary": "Send test ETH (dev mode only)",
        "requestBody": {
          "required": true,
          "content": {"application/json": {"schema": {"$ref": "#/components/schemas/FaucetRequest"}}}
        },
        "responses": {
          "200": {"description": "OK", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/FaucetResponse"}}}},
          "400": {"description": "Invalid request"},
          "500": {"description": "Send failed"}
        }
      }
    },
    "/metrics": {
      "get": {
        "summary": "Prometheus metrics",
        "responses": {"200": {"description": "OK", "content": {"text/plain": {}}}}
      }
    }
  },
  "components": {
    "schemas": {
      "StatusResponse": {
        "type": "object",
        "properties": {
          "node_id": {"type": "string"},
          "moniker": {"type": "string"},
          "chain_id": {"type": "string"},
          "height": {"type": "integer"},
          "latest_block_hash": {"type": "string"},
          "latest_app_hash": {"type": "string"},
          "catching_up": {"type": "boolean"},
          "peers": {"type": "integer"},
          "comet_version": {"type": "string"},
          "validator_address": {"type": "string"},
          "voting_power": {"type": "integer"}
        }
      },
      "ValidatorResponse": {
        "type": "object",
        "properties": {
          "address": {"type": "string"},
          "pub_key": {"type": "string"},
          "voting_power": {"type": "integer"},
          "is_active": {"type": "boolean"}
        }
      },
      "BlocksResponse": {
        "type": "object",
        "properties": {
          "blocks": {"type": "array", "items": {"$ref": "#/components/schemas/BlockSummary"}},
          "total": {"type": "integer"}
        }
      },
      "BlockSummary": {
        "type": "object",
        "properties": {
          "height": {"type": "integer"},
          "hash": {"type": "string"},
          "time": {"type": "string"},
          "proposer": {"type": "string"},
          "tx_count": {"type": "integer"},
          "exec_hash": {"type": "string"}
        }
      },
      "BlockResponse": {
        "type": "object",
        "properties": {
          "height": {"type": "integer"},
          "hash": {"type": "string"},
          "time": {"type": "string"},
          "proposer": {"type": "string"},
          "txs": {"type": "array", "items": {"type": "string"}},
          "exec_hash": {"type": "string"},
          "gas_used": {"type": "string"},
          "gas_limit": {"type": "string"}
        }
      },
      "TxResponse": {
        "type": "object",
        "properties": {
          "hash": {"type": "string"},
          "block_height": {"type": "integer"},
          "status": {"type": "string", "enum": ["success", "reverted", "pending"]},
          "from": {"type": "string"},
          "to": {"type": "string"},
          "value": {"type": "string"},
          "gas_used": {"type": "string"},
          "gas_price": {"type": "string"},
          "nonce": {"type": "integer"}
        }
      },
      "AddressResponse": {
        "type": "object",
        "properties": {
          "address": {"type": "string"},
          "balance": {"type": "string"},
          "nonce": {"type": "integer"},
          "code": {"type": "string"}
        }
      },
      "PeersResponse": {
        "type": "object",
        "properties": {
          "peers": {"type": "array", "items": {"$ref": "#/components/schemas/Peer"}}
        }
      },
      "Peer": {
        "type": "object",
        "properties": {
          "id": {"type": "string"},
          "remote_ip": {"type": "string"},
          "is_outbound": {"type": "boolean"},
          "moniker": {"type": "string"}
        }
      },
      "FaucetRequest": {
        "type": "object",
        "required": ["address"],
        "properties": {
          "address": {"type": "string", "example": "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266"}
        }
      },
      "FaucetResponse": {
        "type": "object",
        "properties": {
          "tx_hash": {"type": "string"},
          "amount": {"type": "string"},
          "message": {"type": "string"}
        }
      }
    }
  }
}`)
}
