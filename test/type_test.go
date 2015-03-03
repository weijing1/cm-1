package test

import (
	"math"

	. "gopkg.in/check.v1"
)

type TypeTestSuit struct {
	createStmts map[string]string
}

var _ = Suite(&TypeTestSuit{
	createStmts: map[string]string{
		"tbl_int":     `create table tbl_int (id int, data int)`,
		"tbl_double":  `create table tbl_double (id int, data double)`,
		"tbl_blob":    `create table tbl_blob (id int, data blob)`,
		"tbl_text":    `create table tbl_text (id int, data text)`,
		"tbl_varchar": `create table tbl_varchar (id int, data varchar(50))`,
	},
})

func (s *TypeTestSuit) SetUpTest(c *C) {
	dropTables()
	for _, stmt := range s.createStmts {
		mustExec(mysqlDB, stmt)
	}
	reloadConfig()
}

func (s *TypeTestSuit) TearDownTest(c *C) {
	dropTables()
}

func (s *TypeTestSuit) TestInt(c *C) {
	// insert
	r := mustExec(proxyDB, "insert into tbl_int values(1, 100)")
	rowsAffected, err := r.RowsAffected()
	c.Assert(err, Equals, nil)
	c.Assert(rowsAffected, Equals, int64(1))

	// query
	rs := mustQuery(proxyDB, "select data from tbl_int where id = 1")
	defer rs.Close()
	for rs.Next() {
		var data int
		err := rs.Scan(&data)
		c.Assert(err, Equals, nil)
		c.Assert(data, Equals, 100)
	}

	// update
	mustExec(proxyDB, "update tbl_int set data = 200 where id = 1")
	rs = mustQuery(proxyDB, "select data from tbl_int where id = 1")
	defer rs.Close()
	for rs.Next() {
		var data int
		err := rs.Scan(&data)
		c.Assert(err, Equals, nil)
		c.Assert(data, Equals, 200)
	}

	// remove
	r = mustExec(proxyDB, "delete from tbl_int where id = 1")
	rowsAffected, err = r.RowsAffected()
	c.Assert(err, Equals, nil)
	c.Assert(rowsAffected, Equals, int64(1))
}

func (s *TypeTestSuit) TestDouble(c *C) {
	// insert
	r := mustExec(proxyDB, "insert into tbl_double values(1, 100.5)")
	rowsAffected, err := r.RowsAffected()
	c.Assert(err, Equals, nil)
	c.Assert(rowsAffected, Equals, int64(1))

	// query
	rs := mustQuery(proxyDB, "select data from tbl_double where id = 1")
	defer rs.Close()

	for rs.Next() {
		var data float64
		err := rs.Scan(&data)
		c.Assert(err, Equals, nil)
		c.Assert(math.Abs(data-100.5) <= 1e-7, Equals, true)
	}

	// update
	mustExec(proxyDB, "update tbl_double set data = 200.5 where id = 1")
	rs = mustQuery(proxyDB, "select data from tbl_double where id = 1")
	defer rs.Close()
	for rs.Next() {
		var data float64
		err := rs.Scan(&data)
		c.Assert(err, Equals, nil)
		c.Assert(math.Abs(data-200.5) <= 1e-7, Equals, true)
	}

	// remove
	r = mustExec(proxyDB, "delete from tbl_double where id = 1")
	rowsAffected, err = r.RowsAffected()
	c.Assert(err, Equals, nil)
	c.Assert(rowsAffected, Equals, int64(1))
}

func (s *TypeTestSuit) TestVarchar(c *C) {
	// insert
	r := mustExec(proxyDB, "insert into tbl_varchar values(1, 'hello')")
	rowsAffected, err := r.RowsAffected()
	c.Assert(err, Equals, nil)
	c.Assert(rowsAffected, Equals, int64(1))

	// query
	rs := mustQuery(proxyDB, "select data from tbl_varchar where id = 1")
	defer rs.Close()

	for rs.Next() {
		var data string
		err := rs.Scan(&data)
		c.Assert(err, Equals, nil)
		c.Assert(data, Equals, "hello")
	}

	// update
	mustExec(proxyDB, "update tbl_varchar set data = 'world' where id = 1")
	rs = mustQuery(proxyDB, "select data from tbl_varchar where id = 1")
	defer rs.Close()
	for rs.Next() {
		var data string
		err := rs.Scan(&data)
		c.Assert(err, Equals, nil)
		c.Assert(data, Equals, "world")
	}

	// remove
	r = mustExec(proxyDB, "delete from tbl_varchar where id = 1")
	rowsAffected, err = r.RowsAffected()
	c.Assert(err, Equals, nil)
	c.Assert(rowsAffected, Equals, int64(1))
}