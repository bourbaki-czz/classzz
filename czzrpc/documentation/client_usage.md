# Client usage

Clients use gRPC to interact with the node.  A client may be implemented in any
language directly supported by [gRPC](http://www.grpc.io/), languages capable of
performing [FFI](https://en.wikipedia.org/wiki/Foreign_function_interface) with
these, and languages that share a common runtime (e.g. Scala, Kotlin, and Ceylon
for the JVM, F# for the CLR, etc.).  Exact instructions differ slightly
depending on the language being used, but the general process is the same for
each.  In short summary, to call RPC server methods, a client must:

1. Generate the language-specific client bindings using the `protoc` compiler and [czzrpc.proto](.../czzrpc.proto)
2. Import or include the gRPC dependency
3. (Optional) Wrap the client bindings with application-specific types
4. Open a gRPC channel using the server's self-signed TLS certificate or a valid TLS certificate.

The only exception to these steps is if the client is being written in Go.  In
that case, the first step may be omitted by importing the bindings from
classzz itself.

The rest of this document provides short examples of how to quickly get started
by implementing a basic client that fetches the balance of the default account
(account 0) from a testnet3 server listening on `localhost:18335` in several
different languages:

- [Go](#go)

Unless otherwise stated under the language example, it is assumed that
gRPC is already already installed.  The gRPC installation procedure
can vary greatly depending on the operating system being used and
whether a gRPC source install is required.  Follow the [gRPC install
instructions](https://github.com/grpc/grpc/blob/master/INSTALL) if
gRPC is not already installed.  A full gRPC install also includes
[Protocol Buffers](https://github.com/google/protobuf) (compiled with
support for the proto3 language version), which contains the protoc
tool and language plugins used to compile this project's `.proto`
files to language-specific bindings.

##TLS
By default classzz uses a self signed certificate to encrypt and authenticate the
connection. To authenticate against the server the client will need access to the
certificate. For example, in Go:
```go
certificateFile := filepath.Join(czzutil.AppDataDir("czzwallet", false), "rpc.cert")
creds, err := credentials.NewClientTLSFromFile(certificateFile, "localhost")
if err != nil {
    fmt.Println(err)
    return
}
tlsOption := grpc.WithTransportCredentials(creds)
```

If the server is using a certificate signed by a valid certificate authority just use nil for the cert:
```go
tlsOption := gprc.WithTransportCredentials(credentials.NewClientTLSFromCert(nil, "")
```

## Authentication

The server may require client authentication via an auth token. This token must be provided with each request as part of the context metadata. 
The key is `AuthenticationToken`. For example in Go:
```go
md := metadata.Pairs("AuthenticationToken", "auth_token_here")
ctx := metadata.NewOutgoingContext(context.Background(), md)

// Make the RPC
response, err := client.SomeRPC(ctx, someRequest)
```

## Go

The native gRPC library (gRPC Core) is not required for Go clients (a
pure Go implementation is used instead) and no additional setup is
required to generate Go bindings.

```Go
package main

import (
	"fmt"
	"path/filepath"

	pb "github.com/bourbaki-czz/classzz/czzrpc/pb"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/bourbaki-czz/czzutil"
)

var certificateFile = filepath.Join(czzutil.AppDataDir("classzz", false), "rpc.cert")

func main() {
	creds, err := credentials.NewClientTLSFromFile(certificateFile, "localhost")
	if err != nil {
		fmt.Println(err)
		return
	}
	conn, err := grpc.Dial("localhost:18332", grpc.WithTransportCredentials(creds))
	if err != nil {
		fmt.Println(err)
		return
	}
	defer conn.Close()
	c := pb.NewCzzrpcClient(conn)
	
	blockchainInfoResp, err := c.GetBlockchainInfo(context.Background(), &pb.GetBlockchainInfoRequest{})
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println("Blockchain Height: ", blockchainInfoResp.BestHeight)
}
```

TODO: Add examples in other languages
