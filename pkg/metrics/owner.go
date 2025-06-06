// Copyright 2018 PingCAP, Inc.
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

package metrics

import (
	metricscommon "github.com/pingcap/tidb/pkg/metrics/common"
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics
var (
	NewSessionHistogram *prometheus.HistogramVec

	WatcherClosed     = "watcher_closed"
	Cancelled         = "cancelled"
	Deleted           = "deleted"
	PutValue          = "put_value"
	SessionDone       = "session_done"
	CtxDone           = "context_done"
	WatchOwnerCounter *prometheus.CounterVec

	NoLongerOwner        = "no_longer_owner"
	CampaignOwnerCounter *prometheus.CounterVec
)

// InitOwnerMetrics initializes owner metrics.
func InitOwnerMetrics() {
	NewSessionHistogram = metricscommon.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "tidb",
			Subsystem: "owner",
			Name:      "new_session_duration_seconds",
			Help:      "Bucketed histogram of processing time (s) of new session.",
			Buckets:   prometheus.ExponentialBuckets(0.0005, 2, 22), // 0.5ms ~ 1048s
		}, []string{LblType, LblResult})

	WatchOwnerCounter = metricscommon.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "tidb",
			Subsystem: "owner",
			Name:      "watch_owner_total",
			Help:      "Counter of watch owner.",
		}, []string{LblType, LblResult})

	CampaignOwnerCounter = metricscommon.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "tidb",
			Subsystem: "owner",
			Name:      "campaign_owner_total",
			Help:      "Counter of campaign owner.",
		}, []string{LblType, LblResult})
}
