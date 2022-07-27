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
	"fmt"

	"k8s.io/klog"
)

func (d *dbConn) DBInit() {
	db := d.db
	klog.V(3).Info("Initializing v1beta1 DB schema")

	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS observation_logs
		(trial_name VARCHAR(255) NOT NULL,
		id INT AUTO_INCREMENT PRIMARY KEY,
		time DATETIME(6),
		metric_name VARCHAR(255) NOT NULL,
		value TEXT NOT NULL)`)
	if err != nil {
		klog.V(0).Infof("Error creating observation_logs table: %v", err)
	}
}

func (d *dbConn) SelectOne() error {
	db := d.db
	_, err := db.Exec(`SELECT 1`)
	if err != nil {
		return fmt.Errorf("Error `SELECT 1` probing: %v", err)
	}
	return nil
}
