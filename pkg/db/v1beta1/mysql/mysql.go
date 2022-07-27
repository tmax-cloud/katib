/*
Copyright 2021 The Kubeflow Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package mysql

import (
	crand "crypto/rand"
	"database/sql"
	"fmt"
	"math/big"
	"math/rand"
	"os"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"k8s.io/klog"

	v1beta1 "github.com/kubeflow/katib/pkg/apis/manager/v1beta1"
	"github.com/kubeflow/katib/pkg/db/v1beta1/common"
	"github.com/kubeflow/katib/pkg/util/v1beta1/env"
)

const (
	dbDriver = "mysql"
	//dbNameTmpl   = "root:%s@tcp(%s:%s)/%s?timeout=5s"
	dbNameTmpl   = "%s:%s@tcp(%s:%s)/%s?timeout=5s"
	mysqlTimeFmt = "2006-01-02 15:04:05.999999"

	connectInterval = 5 * time.Second
	connectTimeout  = 60 * time.Second
)

type dbConn struct {
	db *sql.DB
}

func getDbName() string {
	dbPassEnvName := common.DBPasswordEnvName
	dbPass := os.Getenv(dbPassEnvName)
	dbUser := env.GetEnvOrDefault(
		common.DBUserEnvName, common.DefaultMySQLUser)
	dbHost := env.GetEnvOrDefault(
		common.MySQLDBHostEnvName, common.DefaultMySQLHost)
	dbPort := env.GetEnvOrDefault(
		common.MySQLDBPortEnvName, common.DefaultMySQLPort)
	dbName := env.GetEnvOrDefault(common.MySQLDatabase,
		common.DefaultMySQLDatabase)

	return fmt.Sprintf(dbNameTmpl, dbUser, dbPass, dbHost, dbPort, dbName)
}

func openSQLConn(driverName string, dataSourceName string, interval time.Duration,
	timeout time.Duration) (*sql.DB, error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	timeoutC := time.After(timeout)
	for {
		select {
		case <-ticker.C:
			if db, err := sql.Open(driverName, dataSourceName); err == nil {
				if err = db.Ping(); err == nil {
					return db, nil
				}
				klog.V(1).Infof("Ping to Katib db failed: %v", err)
			} else {
				klog.V(1).Infof("Open sql connection failed: %v", err)
			}
		case <-timeoutC:
			return nil, fmt.Errorf("Timeout waiting for DB conn successfully opened.")
		}
	}
}

func NewWithSQLConn(db *sql.DB) (common.KatibDBInterface, error) {
	d := new(dbConn)
	d.db = db
	seed, err := crand.Int(crand.Reader, big.NewInt(1<<63-1))
	if err != nil {
		return nil, fmt.Errorf("RNG initialization failed: %v", err)
	}
	// We can do the following instead, but it creates a locking issue
	//d.rng = rand.New(rand.NewSource(seed.Int64()))
	rand.Seed(seed.Int64())

	return d, nil
}

func NewDBInterface() (common.KatibDBInterface, error) {
	db, err := openSQLConn(dbDriver, getDbName(), connectInterval, connectTimeout)
	if err != nil {
		return nil, fmt.Errorf("DB open failed: %v", err)
	}
	return NewWithSQLConn(db)
}

func (d *dbConn) RegisterObservationLog(trialName string, observationLog *v1beta1.ObservationLog) error {
	sqlQuery := "INSERT INTO observation_logs (trial_name, time, metric_name, value) VALUES "
	values := []interface{}{}

	for _, mlog := range observationLog.MetricLogs {
		if mlog.TimeStamp == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, mlog.TimeStamp)
		if err != nil {
			return fmt.Errorf("Error parsing start time %s: %v", mlog.TimeStamp, err)
		}
		sqlTimeStr := t.UTC().Format(mysqlTimeFmt)

		sqlQuery += "(?, ?, ?, ?),"
		values = append(values, trialName, sqlTimeStr, mlog.Metric.Name, mlog.Metric.Value)
	}
	sqlQuery = sqlQuery[0 : len(sqlQuery)-1]

	// Prepare the statement
	stmt, err := d.db.Prepare(sqlQuery)
	if err != nil {
		return fmt.Errorf("Pepare SQL statement failed: %v", err)
	}

	// Execute INSERT
	_, err = stmt.Exec(values...)
	if err != nil {
		return fmt.Errorf("Execute SQL INSERT failed: %v", err)
	}

	return nil
}

func (d *dbConn) DeleteObservationLog(trialName string) error {
	_, err := d.db.Exec("DELETE FROM observation_logs WHERE trial_name = ?", trialName)
	return err
}

func (d *dbConn) GetObservationLog(trialName string, metricName string, startTime string, endTime string) (*v1beta1.ObservationLog, error) {
	qfield := []interface{}{trialName}
	qstr := ""
	if metricName != "" {
		qstr += " AND metric_name = ?"
		qfield = append(qfield, metricName)
	}
	if startTime != "" {
		s_time, err := time.Parse(time.RFC3339Nano, startTime)
		if err != nil {
			return nil, fmt.Errorf("Error parsing start time %s: %v", startTime, err)
		}
		formattedStartTime := s_time.UTC().Format(mysqlTimeFmt)
		qstr += " AND time >= ?"
		qfield = append(qfield, formattedStartTime)
	}
	if endTime != "" {
		e_time, err := time.Parse(time.RFC3339Nano, endTime)
		if err != nil {
			return nil, fmt.Errorf("Error parsing completion time %s: %v", endTime, err)
		}
		formattedEndTime := e_time.UTC().Format(mysqlTimeFmt)
		qstr += " AND time <= ?"
		qfield = append(qfield, formattedEndTime)
	}
	rows, err := d.db.Query("SELECT time, metric_name, value FROM observation_logs WHERE trial_name = ?"+qstr+" ORDER BY time",
		qfield...)
	if err != nil {
		return nil, fmt.Errorf("Failed to get ObservationLogs %v", err)
	}
	result := &v1beta1.ObservationLog{
		MetricLogs: []*v1beta1.MetricLog{},
	}
	for rows.Next() {
		var mname, mvalue, sqlTimeStr string
		err := rows.Scan(&sqlTimeStr, &mname, &mvalue)
		if err != nil {
			klog.V(1).Infof("Error scanning log: %v", err)
			continue
		}
		ptime, err := time.Parse(mysqlTimeFmt, sqlTimeStr)
		if err != nil {
			klog.V(1).Infof("Error parsing time %s: %v", sqlTimeStr, err)
			continue
		}
		timeStamp := ptime.UTC().Format(time.RFC3339Nano)
		result.MetricLogs = append(result.MetricLogs, &v1beta1.MetricLog{
			TimeStamp: timeStamp,
			Metric: &v1beta1.Metric{
				Name:  mname,
				Value: mvalue,
			},
		})
	}
	return result, nil
}
