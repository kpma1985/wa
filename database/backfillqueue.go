// mautrix-whatsapp - A Matrix-WhatsApp puppeting bridge.
// Copyright (C) 2021 Tulir Asokan, Sumner Evans
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package database

import (
	"database/sql"
	"errors"
	"time"

	log "maunium.net/go/maulogger/v2"
	"maunium.net/go/mautrix/id"
)

type BackfillType int

const (
	BackfillImmediate BackfillType = 0
	BackfillDeferred               = 1
)

type BackfillQuery struct {
	db  *Database
	log log.Logger
}

func (bq *BackfillQuery) New() *Backfill {
	return &Backfill{
		db:     bq.db,
		log:    bq.log,
		Portal: &PortalKey{},
	}
}

func (bq *BackfillQuery) NewWithValues(userID id.UserID, backfillType BackfillType, priority int, portal *PortalKey, timeStart *time.Time, timeEnd *time.Time, maxBatchEvents, maxTotalEvents, batchDelay int) *Backfill {
	return &Backfill{
		db:             bq.db,
		log:            bq.log,
		UserID:         userID,
		BackfillType:   backfillType,
		Priority:       priority,
		Portal:         portal,
		TimeStart:      timeStart,
		TimeEnd:        timeEnd,
		MaxBatchEvents: maxBatchEvents,
		MaxTotalEvents: maxTotalEvents,
		BatchDelay:     batchDelay,
	}
}

const (
	getNextBackfillQuery = `
		SELECT queue_id, user_mxid, type, priority, portal_jid, portal_receiver, time_start, time_end, max_batch_events, max_total_events, batch_delay
		  FROM backfill_queue
		 WHERE user_mxid=$1
		   AND type=$2
		   AND completed_at IS NULL
	  ORDER BY priority, queue_id
	     LIMIT 1
	`
)

/// Returns the next backfill to perform
func (bq *BackfillQuery) GetNext(userID id.UserID, backfillType BackfillType) (backfill *Backfill) {
	rows, err := bq.db.Query(getNextBackfillQuery, userID, backfillType)
	defer rows.Close()
	if err != nil || rows == nil {
		bq.log.Error(err)
		return
	}
	if rows.Next() {
		backfill = bq.New().Scan(rows)
	}
	return
}

func (bq *BackfillQuery) DeleteAll(userID id.UserID) error {
	_, err := bq.db.Exec("DELETE FROM backfill_queue WHERE user_mxid=$1", userID)
	return err
}

type Backfill struct {
	db  *Database
	log log.Logger

	// Fields
	QueueID        int
	UserID         id.UserID
	BackfillType   BackfillType
	Priority       int
	Portal         *PortalKey
	TimeStart      *time.Time
	TimeEnd        *time.Time
	MaxBatchEvents int
	MaxTotalEvents int
	BatchDelay     int
	CompletedAt    *time.Time
}

func (b *Backfill) Scan(row Scannable) *Backfill {
	err := row.Scan(&b.QueueID, &b.UserID, &b.BackfillType, &b.Priority, &b.Portal.JID, &b.Portal.Receiver, &b.TimeStart, &b.TimeEnd, &b.MaxBatchEvents, &b.MaxTotalEvents, &b.BatchDelay)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			b.log.Errorln("Database scan failed:", err)
		}
		return nil
	}
	return b
}

func (b *Backfill) Insert() {
	rows, err := b.db.Query(`
		INSERT INTO backfill_queue
			(user_mxid, type, priority, portal_jid, portal_receiver, time_start, time_end, max_batch_events, max_total_events, batch_delay, completed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING queue_id
	`, b.UserID, b.BackfillType, b.Priority, b.Portal.JID, b.Portal.Receiver, b.TimeStart, b.TimeEnd, b.MaxBatchEvents, b.MaxTotalEvents, b.BatchDelay, b.CompletedAt)
	defer rows.Close()
	if err != nil || !rows.Next() {
		b.log.Warnfln("Failed to insert %v/%s with priority %d: %v", b.BackfillType, b.Portal.JID, b.Priority, err)
		return
	}
	err = rows.Scan(&b.QueueID)
	if err != nil {
		b.log.Warnfln("Failed to insert %s/%s with priority %s: %v", b.BackfillType, b.Portal.JID, b.Priority, err)
	}
}

func (b *Backfill) MarkDone() {
	if b.QueueID == 0 {
		b.log.Errorf("Cannot delete backfill without queue_id. Maybe it wasn't actually inserted in the database?")
		return
	}
	_, err := b.db.Exec("UPDATE backfill_queue SET completed_at=$1 WHERE queue_id=$2", time.Now(), b.QueueID)
	if err != nil {
		b.log.Warnfln("Failed to mark %s/%s as complete: %v", b.BackfillType, b.Priority, err)
	}
}
