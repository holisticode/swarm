// Copyright 2018 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package metrics

import (
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/ethereum/go-ethereum/cmd/utils"
	"github.com/ethereum/go-ethereum/metrics"
	"github.com/ethereum/go-ethereum/metrics/prometheus"
	"github.com/holisticode/swarm/log"
	"github.com/holisticode/swarm/metrics/influxdb"
)

type Options struct {
	Endoint       string
	Database      string
	Username      string
	Password      string
	EnableExport  bool
	DataDirectory string
	InfluxDBTags  string
}

func init() {
	registerRuntimeMemStats(metrics.DefaultRegistry)
}

func Setup(o Options) {
	if metrics.Enabled {
		log.Info("Enabling swarm metrics collection")

		// Start system runtime metrics collection
		go metrics.CollectProcessMetrics(4 * time.Second)

		// Start collecting disk metrics
		go datadirDiskUsage(o.DataDirectory, 4*time.Second)

		go captureRuntimeMemStats(metrics.DefaultRegistry, 4*time.Second)

		tagsMap := utils.SplitTagsFlag(o.InfluxDBTags)

		if o.EnableExport {
			log.Info("Enabling swarm metrics export to InfluxDB")
			go influxdb.InfluxDBWithTags(metrics.DefaultRegistry, 10*time.Second, o.Endoint, o.Database, o.Username, o.Password, "swarm.", tagsMap)
			go influxdb.InfluxDBWithTags(metrics.AccountingRegistry, 10*time.Second, o.Endoint, o.Database, o.Username, o.Password, "accounting.", tagsMap)
		}
		http.Handle("/debug/metrics/prometheus/accounting", prometheus.Handler(metrics.AccountingRegistry))
	}
}

func datadirDiskUsage(path string, d time.Duration) {
	for range time.Tick(d) {
		bytes, err := dirSize(path)
		if err != nil {
			log.Trace("cannot get disk space", "err", err)
		}

		metrics.GetOrRegisterGauge("datadir/usage", nil).Update(bytes)
	}
}

func dirSize(path string) (int64, error) {
	var size int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return err
	})
	return size, err
}
