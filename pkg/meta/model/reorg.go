// Copyright 2023 PingCAP, Inc.
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

package model

import (
	"encoding/json"

	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/pkg/parser/mysql"
	"github.com/pingcap/tidb/pkg/parser/terror"
	"github.com/pingcap/tidb/pkg/sessionctx/vardef"
	"go.uber.org/atomic"
)

// BackfillState is the state used by the backfill-merge process.
type BackfillState byte

const (
	// BackfillStateInapplicable means the backfill-merge process is not used.
	BackfillStateInapplicable BackfillState = iota
	// BackfillStateRunning is the state that the backfill process is running.
	// In this state, the index's write and delete operations are redirected to a temporary index.
	BackfillStateRunning
	// BackfillStateReadyToMerge is the state that the temporary index's records are ready to be merged back
	// to the origin index.
	// In this state, the index's write and delete operations are copied to a temporary index.
	// This state is used to make sure that all the TiDB instances are aware of the copy
	// during the merge(BackfillStateMerging).
	BackfillStateReadyToMerge
	// BackfillStateMerging is the state that the temp index is merging back to the origin index.
	// In this state, the index's write and delete operations are copied to a temporary index.
	BackfillStateMerging
)

// String implements fmt.Stringer interface.
func (s BackfillState) String() string {
	switch s {
	case BackfillStateRunning:
		return "backfill state running"
	case BackfillStateReadyToMerge:
		return "backfill state ready to merge"
	case BackfillStateMerging:
		return "backfill state merging"
	case BackfillStateInapplicable:
		return "backfill state inapplicable"
	default:
		return "backfill state unknown"
	}
}

// DDLReorgMeta is meta info of DDL reorganization.
type DDLReorgMeta struct {
	SQLMode           mysql.SQLMode                    `json:"sql_mode"`
	Warnings          map[errors.ErrorID]*terror.Error `json:"warnings"`
	WarningsCount     map[errors.ErrorID]int64         `json:"warnings_count"`
	Location          *TimeZoneLocation                `json:"location"`
	ReorgTp           ReorgType                        `json:"reorg_tp"`
	IsFastReorg       bool                             `json:"is_fast_reorg"`
	IsDistReorg       bool                             `json:"is_dist_reorg"`
	UseCloudStorage   bool                             `json:"use_cloud_storage"`
	ResourceGroupName string                           `json:"resource_group_name"`
	Version           int64                            `json:"version"`
	TargetScope       string                           `json:"target_scope"`
	MaxNodeCount      int                              `json:"max_node_count"`
	// These two variables are used to control the concurrency and batch size of the reorganization process.
	// They can be adjusted dynamically through `admin alter ddl jobs` command.
	// Note: Don't get or set these two variables directly, use the functions instead.
	Concurrency   atomic.Int64 `json:"concurrency"`
	BatchSize     atomic.Int64 `json:"batch_size"`
	MaxWriteSpeed atomic.Int64 `json:"max_write_speed"`
}

// GetConcurrency gets the concurrency from DDLReorgMeta.
func (dm *DDLReorgMeta) GetConcurrency() int {
	concurrency := dm.Concurrency.Load()
	if concurrency == 0 {
		// when the job coming from old cluster, concurrency might not set
		return int(vardef.GetDDLReorgWorkerCounter())
	}
	return int(concurrency)
}

// SetConcurrency sets the concurrency in DDLReorgMeta.
func (dm *DDLReorgMeta) SetConcurrency(concurrency int) {
	dm.Concurrency.Store(int64(concurrency))
}

// GetBatchSize gets the batch size from DDLReorgMeta.
func (dm *DDLReorgMeta) GetBatchSize() int {
	batchSize := dm.BatchSize.Load()
	if batchSize == 0 {
		// when the job coming from old cluster, batch-size might not set
		return int(vardef.GetDDLReorgBatchSize())
	}
	return int(batchSize)
}

// SetBatchSize sets the batch size in DDLReorgMeta.
func (dm *DDLReorgMeta) SetBatchSize(batchSize int) {
	dm.BatchSize.Store(int64(batchSize))
}

// GetMaxWriteSpeed gets the max write speed from DDLReorgMeta.
// 0 means no limit.
func (dm *DDLReorgMeta) GetMaxWriteSpeed() int {
	// 0 means no limit, so it's ok even when the job coming from old cluster
	return int(dm.MaxWriteSpeed.Load())
}

// SetMaxWriteSpeed sets the max write speed in DDLReorgMeta.
func (dm *DDLReorgMeta) SetMaxWriteSpeed(maxWriteSpeed int) {
	dm.MaxWriteSpeed.Store(int64(maxWriteSpeed))
}

const (
	// ReorgMetaVersion0 is the minimum version of DDLReorgMeta.
	ReorgMetaVersion0 = int64(0)
	// CurrentReorgMetaVersion is the current version of DDLReorgMeta.
	// For fix #46306(whether end key is included or not in the table range) to add the version to 1.
	CurrentReorgMetaVersion = int64(1)
)

// ReorgType indicates which process is used for the data reorganization.
type ReorgType int8

const (
	// ReorgTypeNone means the backfill task is not started yet.
	ReorgTypeNone ReorgType = iota
	// ReorgTypeTxn means the index records are backfill with transactions.
	// All the index KVs are written through the transaction interface.
	// This is the original backfill implementation.
	ReorgTypeTxn
	// ReorgTypeLitMerge means the index records are backfill with lightning.
	// The index KVs are encoded to SST files and imported to the storage directly.
	// The incremental index KVs written by DML are redirected to a temporary index.
	// After the backfill is finished, the temporary index records are merged back to the original index.
	ReorgTypeLitMerge
	// ReorgTypeTxnMerge means backfill with transactions and merge incremental changes.
	// The backfill index KVs are written through the transaction interface.
	// The incremental index KVs written by DML are redirected to a temporary index.
	// After the backfill is finished, the temporary index records are merged back to the original index.
	ReorgTypeTxnMerge
)

// NeedMergeProcess means the incremental changes need to be merged.
func (tp ReorgType) NeedMergeProcess() bool {
	return tp == ReorgTypeLitMerge || tp == ReorgTypeTxnMerge
}

// String implements fmt.Stringer interface.
func (tp ReorgType) String() string {
	switch tp {
	case ReorgTypeTxn:
		return "txn"
	case ReorgTypeLitMerge:
		return "ingest"
	case ReorgTypeTxnMerge:
		return "txn-merge"
	}
	return ""
}

// BackfillMeta is meta info of the backfill job.
type BackfillMeta struct {
	IsUnique   bool          `json:"is_unique"`
	EndInclude bool          `json:"end_include"`
	Error      *terror.Error `json:"err"`

	SQLMode       mysql.SQLMode                    `json:"sql_mode"`
	Warnings      map[errors.ErrorID]*terror.Error `json:"warnings"`
	WarningsCount map[errors.ErrorID]int64         `json:"warnings_count"`
	Location      *TimeZoneLocation                `json:"location"`
	ReorgTp       ReorgType                        `json:"reorg_tp"`
	RowCount      int64                            `json:"row_count"`
	StartKey      []byte                           `json:"start_key"`
	EndKey        []byte                           `json:"end_key"`
	CurrKey       []byte                           `json:"curr_key"`
	*JobMeta      `json:"job_meta"`
}

// Encode encodes BackfillMeta with json format.
func (bm *BackfillMeta) Encode() ([]byte, error) {
	b, err := json.Marshal(bm)
	return b, errors.Trace(err)
}

// Decode decodes BackfillMeta from the json buffer.
func (bm *BackfillMeta) Decode(b []byte) error {
	err := json.Unmarshal(b, bm)
	return errors.Trace(err)
}
