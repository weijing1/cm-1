package proxy

import (
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/juju/errors"
	"github.com/ngaut/arena"
	stats "github.com/ngaut/gostats"
	log "github.com/ngaut/logging"
	"github.com/ngaut/tokenlimiter"
	"github.com/wandoulabs/cm/config"
	. "github.com/wandoulabs/cm/mysql"
	"github.com/wandoulabs/cm/vt/tabletserver"
)

var (
	baseConnId uint32 = 10000
)

type Server struct {
	configFile        string
	cfg               *config.Config
	addr              string
	user              string
	password          string
	running           int32
	listener          net.Listener
	nodes             map[string]*Node
	schemas           map[string]*Schema
	autoSchamas       map[string]*tabletserver.SchemaInfo
	rwlock            *sync.RWMutex
	taskQ             chan *execTask
	concurrentLimiter *tokenlimiter.TokenLimiter

	counter *stats.Counters
}

type IServer interface {
	GetSchema(string) *Schema
	GetRowCacheSchema(string) (*tabletserver.SchemaInfo, bool)
	CfgGetPwd() string
	GetToken() *tokenlimiter.Token
	ReleaseToken(token *tokenlimiter.Token)
	GetRWlock() *sync.RWMutex
	GetNode(name string) *Node
	GetNodeNames() []string
	AsynExec(task *execTask)
}

func (s *Server) GetToken() *tokenlimiter.Token {
	return s.concurrentLimiter.Get()
}

func (s *Server) ReleaseToken(token *tokenlimiter.Token) {
	s.concurrentLimiter.Put(token)
}

func (s *Server) GetNode(name string) *Node {
	return s.nodes[name]
}

func (s *Server) GetRowCacheSchema(db string) (*tabletserver.SchemaInfo, bool) {
	si, ok := s.autoSchamas[db]
	return si, ok
}

func (s *Server) GetNodeNames() []string {
	ret := make([]string, 0, len(s.nodes))
	for name, _ := range s.nodes {
		ret = append(ret, name)
	}

	return ret
}

func (s *Server) parseNodes() error {
	cfg := s.cfg
	s.nodes = make(map[string]*Node, len(cfg.Nodes))

	for _, v := range cfg.Nodes {
		if _, ok := s.nodes[v.Name]; ok {
			return errors.Errorf("duplicate node [%s].", v.Name)
		}

		n, err := s.parseNode(v)
		if err != nil {
			return errors.Trace(err)
		}

		s.nodes[v.Name] = n
	}

	return nil
}

func (s *Server) parseNode(cfg config.NodeConfig) (*Node, error) {
	n := &Node{
		server: s,
		cfg:    cfg,
	}
	if len(cfg.Master) == 0 {
		return nil, errors.Errorf("must setting master MySQL node.")
	}

	var err error
	if n.master, err = n.openDB(cfg.Master); err != nil {
		return nil, errors.Trace(err)
	}

	return n, nil
}
func (s *Server) newConn(co net.Conn) *Conn {
	c := &Conn{
		c:            co,
		pkg:          NewPacketIO(co),
		server:       s,
		connectionId: atomic.AddUint32(&baseConnId, 1),
		status:       SERVER_STATUS_AUTOCOMMIT,
		collation:    DEFAULT_COLLATION_ID,
		charset:      DEFAULT_CHARSET,
		alloc:        arena.NewArenaAllocator(8 * 1024),
	}
	c.salt, _ = RandomBuf(20)

	return c
}

func (s *Server) GetRWlock() *sync.RWMutex {
	return s.rwlock
}

func (s *Server) AsynExec(task *execTask) {
	s.taskQ <- task
}

func (s *Server) CfgGetPwd() string {
	return s.cfg.Password
}

func (s *Server) loadSchemaInfo() error {
	if err := s.parseNodes(); err != nil {
		return errors.Trace(err)
	}

	if err := s.parseSchemas(); err != nil {
		return errors.Trace(err)
	}

	for _, v := range s.cfg.Schemas {
		rc := v.RulesConifg
		var overrides []tabletserver.SchemaOverride
		for _, sc := range rc.ShardRule {
			or := tabletserver.SchemaOverride{Name: sc.Table}
			pks := strings.Split(sc.Key, ",")
			for _, pk := range pks {
				or.PKColumns = append(or.PKColumns, strings.TrimSpace(pk))
			}
			or.Cache = &tabletserver.OverrideCacheDesc{Type: sc.RowCacheType, Prefix: or.Name, Table: or.Name}
			overrides = append(overrides, or)
		}

		//fix hard code node
		s.autoSchamas[v.DB] = tabletserver.NewSchemaInfo(s.cfg.RowCacheConf, s.cfg.Nodes[0].Master, s.cfg.User, s.cfg.Password, v.DB, overrides)
	}

	return nil
}

func makeServer(configFile string) *Server {
	cfg, err := config.ParseConfigFile(configFile)
	if err != nil {
		log.Error(err.Error())
		return nil
	}

	s := &Server{
		configFile:        configFile,
		cfg:               cfg,
		addr:              cfg.Addr,
		user:              cfg.User,
		password:          cfg.Password,
		autoSchamas:       make(map[string]*tabletserver.SchemaInfo),
		taskQ:             make(chan *execTask, 100),
		concurrentLimiter: tokenlimiter.NewTokenLimiter(100),
		counter:           stats.NewCounters("stats"),
		rwlock:            &sync.RWMutex{},
	}

	f := func(wg *sync.WaitGroup, rs []interface{}, i int, co *SqlConn, sql string, args []interface{}) {
		r, err := co.Execute(sql, args...)
		if err != nil {
			rs[i] = err
		} else {
			rs[i] = r
		}
		wg.Done()
	}

	for i := 0; i < 100; i++ {
		go func() {
			for task := range s.taskQ {
				f(task.wg, task.rs, task.idx, task.co, task.sql, task.args)
			}
		}()
	}

	return s
}

func NewServer(configFile string) (*Server, error) {
	s := makeServer(configFile)
	s.loadSchemaInfo()

	netProto := "tcp"
	if strings.Contains(netProto, "/") {
		netProto = "unix"
	}

	var err error
	s.listener, err = net.Listen(netProto, s.addr)
	if err != nil {
		return nil, errors.Trace(err)
	}

	log.Infof("Server run MySql Protocol Listen(%s) at [%s]", netProto, s.addr)
	return s, nil
}

func (s *Server) cleanup() {
	for _, si := range s.autoSchamas {
		si.Close()
	}
}

func (s *Server) resetSchemaInfo() {
	s.cleanup()
	s.autoSchamas = make(map[string]*tabletserver.SchemaInfo)
	for _, n := range s.nodes {
		n.Close()
	}

	s.nodes = nil
	s.schemas = nil

	cfg, err := config.ParseConfigFile(s.configFile)
	if err != nil {
		log.Fatal(err.Error())
	}

	s.cfg = cfg
	s.loadSchemaInfo()
}

func (s *Server) HandleReload(w http.ResponseWriter, req *http.Request) {
	s.rwlock.Lock()
	defer s.rwlock.Unlock()

	s.resetSchemaInfo()

	io.WriteString(w, "ok")
}

func (s *Server) Run() error {
	atomic.StoreInt32(&s.running, 1)

	for atomic.LoadInt32(&s.running) == 1 {
		conn, err := s.listener.Accept()
		if err != nil {
			log.Errorf("accept error %s", err.Error())
			return err
		}

		go s.onConn(conn)
	}

	return nil
}

func (s *Server) Close() {
	s.rwlock.Lock()
	defer s.rwlock.Unlock()

	atomic.StoreInt32(&s.running, 0)
	if s.listener != nil {
		s.listener.Close()
	}

	s.cleanup()
}

func (s *Server) onConn(c net.Conn) {
	conn := s.newConn(c)
	/*
		defer func() {
			if err := recover(); err != nil {
				const size = 8192
				buf := make([]byte, size)
				buf = buf[:runtime.Stack(buf, false)]
				log.Errorf("onConn panic %v: %v\n%s", c.RemoteAddr().String(), err, buf)
			}

			conn.Close()
		}()
	*/

	if err := conn.Handshake(); err != nil {
		log.Errorf("handshake error %s", err.Error())
		c.Close()
		return
	}

	conn.Run()
}
