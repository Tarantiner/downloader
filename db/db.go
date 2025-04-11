package db

import (
	"database/sql"
	"github.com/sirupsen/logrus"
	_ "modernc.org/sqlite"
)

var db *sql.DB

type TgFile struct {
	Fid   string `json:"fid"`
	Gid   int64  `json:"gid"`
	Gname string `json:"gname"`
	Mid   int    `json:"mid"`
	Fname string `json:"fname"`
	Dname string `json:"dname"`
	Fpath string `json:"fpath"`
	Fsize int64  `json:"fsize"`
	Ftime string `json:"ftime"`
	Msg   string `json:"msg"`
}

func DbInit(dbPath string) {
	var err error
	db, err = sql.Open("sqlite", dbPath+"?_pragma=journal_mode=WAL&_pragma=busy_timeout(5000)") // 缓解多进程竞争问题，busy_timeout可等另一写入5秒
	if err != nil {
		panic(err)
	}

	// 创建表
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS files (
		fid text PRIMARY KEY,
		gid INTEGER,
		gname TEXT,
		mid INTEGER,
		fname TEXT,
		dname TEXT,
		fpath TEXT,
		fsize INTEGER,
		msg TEXT,
		ftime TIMESTAMP,
		created TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`)
	if err != nil {
		panic(err)
	}

	// 创建表
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_files_dname ON files(dname);`)
	if err != nil {
		panic(err)
	}
}

func DbCheckFile(fname string) ([]*TgFile, error) {
	rows, err := db.Query("SELECT fid, gid, gname, mid, fname, dname, fpath, fsize, msg, ftime FROM files WHERE dname = ?;", fname)
	if err != nil {
		return nil, err
	}
	flis := make([]*TgFile, 0, 3)
	for rows.Next() {
		var file TgFile
		err = rows.Scan(&file.Fid, &file.Gid, &file.Gname, &file.Mid, &file.Fname, &file.Dname, &file.Fpath, &file.Fsize, &file.Msg, &file.Ftime)
		if err != nil {
			continue
		}
		flis = append(flis, &file)
	}
	return flis, nil
}

func DbIsFileExists(logger *logrus.Logger, fid string) bool {
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM files WHERE fid = ?", fid).Scan(&count)
	if err != nil {
		return false
	}
	return count > 0
}

func DbNewFile(logger *logrus.Logger, file *TgFile) bool {
	funcName := "DbNewFile"
	tx, err := db.Begin()
	if err != nil {
		logger.Errorf("%s|begin error: %v", funcName, err.Error())
		return false
	}
	defer func() {
		if err != nil {
			logger.Infof("%s|正在回滚 error: %v", funcName, err.Error())
			tx.Rollback()
		}
	}()

	// 批量插入新记录
	insertStmt, err := tx.Prepare(`
            INSERT or IGNORE INTO files
            (fid, gid, gname, mid, fname, dname, fpath, fsize, msg, ftime) 
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		logger.Errorf("%s|prepare insert error: %v", funcName, err.Error())
		return false
	}
	defer insertStmt.Close()

	_, err = insertStmt.Exec(
		file.Fid,
		file.Gid,
		file.Gname,
		file.Mid,
		file.Fname,
		file.Dname,
		file.Fpath,
		file.Fsize,
		file.Msg,
		file.Ftime,
	)
	if err != nil {
		logger.Warningf("%s|exec insert error|%v: %v", funcName, *file, err.Error())
		return false
	}
	err = tx.Commit() // 注意err作用域
	if err != nil {
		logger.Errorf("%s|commit error: %v", funcName, err.Error())
		return false
	}
	return true
}
