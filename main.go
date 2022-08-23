package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/go-mysql-org/go-mysql/client"
	. "github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/server"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

//const mongoUrl = "mongodb://root:123456@127.0.0.1:27017"

var realMysqlAddr = flag.String("addr", "", "real mysql addr,host:port")
var realMysqlUser = flag.String("user", "", "real mysql user")
var realMysqlPass = flag.String("pass", "", "real mysql pass")
var realMysqlDb = flag.String("db", "capture", "real mysql dbname")
var listenPort = flag.Int("port", 5235, "listenning port")
var mongoUrl = flag.String("mongo", "mongodb://root:123456@127.0.0.1:27017", "mongo address")

var r, _ = regexp.Compile("(?i)(password)[\t ]*")
var r1, _ = regexp.Compile("[\t ]")

func main() {
	test()
}

type RemoteThrottleProvider struct {
	*server.InMemoryProvider
	delay int // in milliseconds
}

type Sql struct {
	User       string `bson:"user" json:"user"`
	Db         string `bson:"db" json:"db"`
	Sql        string `bson:"sql" json:"sql"`
	CreateTime int64  `bson:"create_time" json:"create_time"`
	TakeTime   int64  `bson:"take_time" json:"take_time"`
}

func test() {
	flag.Parse()
	l, err := net.Listen("tcp", ":"+strconv.Itoa(*listenPort))
	if err != nil {
		log.Fatal(err)
	}
	//remoteProvider := &RemoteThrottleProvider{server.NewInMemoryProvider(), 10 + 50}

	auth := NewUserAuth(*mongoUrl)

	//remoteProvider.AddUser("root", "123")
	//remoteProvider.AddUser("admin", "456")

	for {
		c, err := l.Accept()
		if err != nil {
			log.Fatal(err)
		}
		go handler(c, auth)
	}

}

func handler(c net.Conn, auth *UserAuth) {
	newHandler, err := NewHandler(*realMysqlAddr, *realMysqlUser, *realMysqlPass, *realMysqlDb, auth)
	if err != nil {
		c.Write([]byte("proxy fail"))
		return
	}
	defer newHandler.Close()
	svr := server.NewDefaultServer()
	auth.Lock()
	conn, err := server.NewCustomizedConn(c, svr, auth.remoteProvider, newHandler)
	auth.Unlock()
	if err != nil {
		return
	}
	newHandler.SetUser(conn.GetUser())
	newHandler.handlerConn(conn)
	/*
		for {
			err := conn.HandleCommand()
			if err != nil {
				return
			}
		}
	*/
}

type EmptyHandler struct {
	sync.RWMutex
	db_name    string
	userAuth   *UserAuth
	collection *mongo.Collection
	conn       *client.Conn
	user       string
}

func NewHandler(url, user, pass, db string, auth *UserAuth) (*EmptyHandler, error) {
	clientOptions := options.Client().ApplyURI(auth.mongoUrl)
	mClient, err := mongo.Connect(context.TODO(), clientOptions)
	if err != nil {
		return nil, err
	}
	collection := mClient.Database("mysql_proxy").Collection("sql_log")

	conn, err := client.Connect(url, user, pass, db)
	return &EmptyHandler{
		db_name:    db,
		userAuth:   auth,
		collection: collection,
		user:       "null",
		conn:       conn,
	}, err
}

func (m *EmptyHandler) insertMongo(sql Sql) error {
	_, err := m.collection.InsertOne(context.TODO(), sql)
	return err
}

func (h *EmptyHandler) Close() {
	h.collection.Database().Client().Disconnect(context.TODO())
	h.conn.Close()
}

func (h *EmptyHandler) SetUser(user string) {
	h.Lock()
	defer h.Unlock()
	h.user = user
}

func (h *EmptyHandler) GetUser() string {
	h.Lock()
	defer h.Unlock()
	return h.user
}

func (h *EmptyHandler) checkPass(query string) {
	if r.MatchString(query) {
		pass := r.ReplaceAllString(query, "")
		pass = r1.ReplaceAllString(pass, "")
		if err := h.userAuth.updatePass(h.GetUser(), pass); err != nil {
			log.Println(err)
		}
	}
}

func (h *EmptyHandler) handlerConn(sConn *server.Conn) {
	close := make(chan int, 2)
	finish := make(chan int, 2)
	go func() {
		defer func() { finish <- 0 }()
		for {
			select {
			case <-close:
			default:
				buff := make([]byte, 1024)
				//b, err := Cconn.ReadPacket()
				n, err := sConn.Read(buff)
				if err != nil || n < 5 {
					close <- 0
					return
				}
				str := string(buff[5:n])
				log.Println(str)
				if _, err = h.conn.Write(buff[:n]); err != nil {
					close <- 0
					return
				}
				if !r1.MatchString(str) {
					h.db_name = str
				} else {
					err = h.insertMongo(Sql{
						User:       h.GetUser(),
						Db:         h.db_name,
						Sql:        str,
						CreateTime: time.Now().Unix(),
						TakeTime:   0,
					})
				}
			}
		}
	}()
	go func() {
		defer func() { finish <- 0 }()
		for {
			select {
			case <-close:
			default:
				buff := make([]byte, 1024)
				//b, err := Cconn.ReadPacket()
				n, err := h.conn.Read(buff)
				if err != nil {
					close <- 0
					return
				}
				//log.Println(string(buff))
				if _, err = sConn.Write(buff[:n]); err != nil {
					close <- 0
					return
				}
			}
		}
	}()
	<-finish
}

func (h *EmptyHandler) UseDB(dbName string) error {
	_, err := h.conn.Execute("use " + dbName + ";")
	return err
}
func (h *EmptyHandler) HandleQuery(query string) (*Result, error) {
	log.Println(h.GetUser(), query)
	h.checkPass(query)
	start := time.Now().UnixNano()
	r, err := h.conn.Execute(query)
	if err != nil {
		return nil, err
	}
	err = h.insertMongo(Sql{
		User:       h.user,
		Db:         h.conn.GetDB(),
		Sql:        query,
		CreateTime: time.Now().Unix(),
		TakeTime:   time.Now().UnixNano() - start,
	})
	return r, err
}

func (h *EmptyHandler) HandleFieldList(table string, fieldWildcard string) ([]*Field, error) {
	return nil, fmt.Errorf("not supported now")
}
func (h *EmptyHandler) HandleStmtPrepare(query string) (int, int, interface{}, error) {
	return 0, 0, nil, fmt.Errorf("not supported now")
}
func (h *EmptyHandler) HandleStmtExecute(context interface{}, query string, args []interface{}) (*Result, error) {
	return nil, fmt.Errorf("not supported now")
}

func (h *EmptyHandler) HandleStmtClose(context interface{}) error {
	return nil
}

func (h *EmptyHandler) HandleOtherCommand(cmd byte, data []byte) error {
	return NewError(
		ER_UNKNOWN_ERROR,
		fmt.Sprintf("command %d is not supported now", cmd),
	)
}

type User struct {
	Name   string   `bson:"user" json:"user"`
	Pass   string   `bson:"pass" json:"pass"`
	DBList []string `bson:"db_list" json:"db_list"`
}

type UserAuth struct {
	sync.Mutex
	mongoUrl       string
	remoteProvider *RemoteThrottleProvider
}

func NewUserAuth(url string) *UserAuth {
	res := &UserAuth{
		mongoUrl:       url,
		remoteProvider: &RemoteThrottleProvider{server.NewInMemoryProvider(), 10 + 50},
	}
	for _, user := range res.GetUser() {
		fmt.Println(user.Name)
		res.remoteProvider.AddUser(user.Name, user.Pass)
	}
	return res
}

func (u *UserAuth) collection() *mongo.Collection {
	clientOptions := options.Client().ApplyURI(*mongoUrl)
	mClient, err := mongo.Connect(context.TODO(), clientOptions)
	if err != nil {
		log.Fatal(err)
	}
	return mClient.Database("mysql").Collection("user")
}
func (u *UserAuth) GetUser() []User {
	res := new([]User)
	col := u.collection()
	defer col.Database().Client().Disconnect(context.TODO())
	cur, err := col.Find(context.TODO(), bson.D{})
	if err != nil {
		log.Fatal(err)
	}

	err = cur.All(context.TODO(), res)
	if err != nil {
		log.Fatal(err)
	}
	return *res
}

func (u *UserAuth) updatePass(user, pass string) error {
	col := u.collection()
	defer col.Database().Client().Disconnect(context.TODO())
	filter := bson.D{{Key: "user", Value: user}}
	update := bson.D{{Key: "$set", Value: bson.D{{Key: "pass", Value: pass}}}}
	_, err := col.UpdateOne(context.TODO(), filter, update)
	u.Lock()
	defer u.Unlock()
	u.remoteProvider.AddUser(user, pass)
	return err
}
