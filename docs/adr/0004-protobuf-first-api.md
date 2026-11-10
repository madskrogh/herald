# Protobuf-first API with grpc-gateway

The Tenant API is defined once in `.proto` files with `google.api.http` annotations. `buf` generates the gRPC server, a REST/JSON gateway (grpc-gateway) and an OpenAPI spec from them, and both protocols call the same Go service implementation. Tenants get conventional REST URLs, gRPC callers get a typed contract, and the documentation can't drift from the code.

## Considered Options

- **Connect (connectrpc.com)**: lighter, and built directly on `net/http`, but its JSON protocol is RPC-style POSTs, not REST with resource URLs.
- **Hand-written REST and gRPC on a shared service layer**: rejected. Every endpoint would be written twice, and the two would drift.
