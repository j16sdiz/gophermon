package mapsql

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

type DbConnection struct {
	Username string
	Password string
	Host     string
	Port     int
	Database string
}

func (d DbConnection) AddPokemon(encounterId, spawnpointId string, pokemonId int, latitude, longitude float64, disappearTime time.Time) error {
	insert := fmt.Sprintf("INSERT INTO `pokemon` (encounter_id,spawnpoint_id,pokemon_id,latitude,longitude,disappear_time) "+
		"VALUES ('%s', '%s', %d, %.14f, %.14f, '%s')", encounterId, spawnpointId, pokemonId, latitude, longitude, disappearTime.Format("2006-01-2 15:04:05"))
	_, err := d.ExecuteStatement(insert)
	return err
}

func (d DbConnection) AddScannedLocation(latitude, longitude float64) error {
	insert := fmt.Sprintf("INSERT INTO scannedlocation (latitude, longitude, last_modified) VALUES (%f, %f, '%s')", latitude, longitude, time.Now().UTC().Format("2006-01-2 15:04:05"))
	_, err := d.ExecuteStatement(insert)
	if err != nil {
		insert = fmt.Sprintf("UPDATE scannedlocation SET last_modified = '%s' WHERE latitude=%f and longitude=%f", time.Now().UTC().Format("2006-01-2 15:04:05"), latitude, longitude)
		_, err = d.ExecuteStatement(insert)
	}
	return err
}

func (d DbConnection) ExecuteQuery(query string) (*sql.Rows, error) {
	db, err := openDb(d)
	if err != nil {
		return nil, err
	}
	return db.Query(query)
}

func (d DbConnection) ExecuteStatement(statement string) (sql.Result, error) {
	db, err := openDb(d)
	if err != nil {
		return nil, err
	}
	result, err := db.Exec(statement)
	db.Close()
	return result, err
}

func openDb(conn DbConnection) (*sql.DB, error) {
	sourceName := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s",
		conn.Username, conn.Password, conn.Host, conn.Port, conn.Database)
	return sql.Open("mysql", sourceName)
}
