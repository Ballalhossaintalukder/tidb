// Copyright 2025 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ingestcli

import (
	"context"

	"github.com/pingcap/kvproto/pkg/import_sstpb"
	"github.com/pingcap/tidb/br/pkg/restore/split"
)

// WriteRequest is the request to write KV to storage layer.
type WriteRequest struct {
	Pairs []*import_sstpb.Pair
}

// WriteResponse is the response of Write.
type WriteResponse struct {
	nextGenSSTMeta *nextGenSSTMeta
}

// IngestRequest is the request to ingest SST to storage layer.
type IngestRequest struct {
	Region    *split.RegionInfo
	WriteResp *WriteResponse
}

// WriteClient is the client for writing KV to storage layer.
// you can call Write multiple times to write data in a stream way before close.
// when close, the server will return the info of SSTs generated by the server.
// It is caller's responsibility to call Close() when it meets an error.
type WriteClient interface {
	Write(*WriteRequest) error
	Recv() (*WriteResponse, error)
	Close()
}

// Client is the interface to write KV and ingest SST to storage layer.
// the calling sequence of this interface is:
//
//	writeCli := cli.WriteClient(xxx)
//	for haveMoreData {
//	   err := writeCli.Write(xxx)
//	   // handle err, if all data are sent, break
//	}
//	resp, err := writeCli.Close(xxx)
//	// handle err, if everything is ok, start ingest the SSTs
//	cli.Ingest(xxx)
type Client interface {
	// WriteClient returns a WriteClient to write KV to storage layer.
	// WriteClient methods share the same context passed here.
	WriteClient(ctx context.Context, commitTS uint64) (WriteClient, error)
	// Ingest ingests the SST to storage layer.
	Ingest(ctx context.Context, in *IngestRequest) error
}
