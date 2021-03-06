package tcp

import (
	"errors"
	"log"
	"net"
	"os"
	"time"
)

const (
	//tcp conn max packet size
	defaultMaxPacketSize = 1024 << 10 //1MB

	readChanSize  = 10
	writeChanSize = 10
)

var (
	logger *log.Logger
)

func init() {
	logger = log.New(os.Stdout, "", log.Lshortfile)
}

//TCPServer 结构定义
type TCPServer struct {
	//TCP address to listen on
	tcpAddr string

	//the listener
	listener *net.TCPListener

	//callback is an interface
	//it's used to process the connect establish, close and data receive
	callback CallBack
	protocol Protocol

	//if srv is shutdown, close the channel used to inform all session to exit.
	exitChan chan struct{}

	maxPacketSize uint32        //single packet max bytes
	readDeadline  time.Duration //conn read deadline
	bucket        *TCPConnBucket
}

//NewTCPServer 返回一个TCPServer实例
func NewTCPServer(tcpAddr string, callback CallBack, protocol Protocol) *TCPServer {
	return &TCPServer{
		tcpAddr:  tcpAddr,
		callback: callback,
		protocol: protocol,

		bucket:        NewTCPConnBucket(),
		exitChan:      make(chan struct{}),
		maxPacketSize: defaultMaxPacketSize,
	}
}

//ListenAndServe 使用TCPServer的tcpAddr创建TCPListner并调用Server()方法开启监听
func (srv *TCPServer) ListenAndServe() error {
	tcpAddr, err := net.ResolveTCPAddr("tcp4", srv.tcpAddr)
	if err != nil {
		return err
	}
	ln, err := net.ListenTCP("tcp4", tcpAddr)
	if err != nil {
		return err
	}
	return srv.Serve(ln)
}

//Serve 使用指定的TCPListener开启监听
func (srv *TCPServer) Serve(l *net.TCPListener) error {
	srv.listener = l
	defer func() {
		if r := recover(); r != nil {
			log.Println("Serve error", r)
		}
		srv.listener.Close()
	}()

	//清理无效连接
	go func() {
		for {
			srv.removeClosedTCPConn()
			time.Sleep(time.Millisecond * 10)
		}
	}()

	var tempDelay time.Duration
	for {
		select {
		case <-srv.exitChan:
			return errors.New("TCPServer Closed")
		default:
		}
		conn, err := srv.listener.AcceptTCP()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else {
					tempDelay *= 2
				}
				if max := 1 * time.Second; tempDelay > max {
					tempDelay = max
				}
				time.Sleep(tempDelay)
				continue
			}
			log.Println("ln error:", err.Error())
			return err
		}
		tempDelay = 0
		tcpConn := srv.newTCPConn(conn, srv.callback, srv.protocol)
		srv.bucket.Put(tcpConn.RemoteAddr(), tcpConn)
	}
}

func (srv *TCPServer) newTCPConn(conn *net.TCPConn, callback CallBack, protocol Protocol) *TCPConn {
	if callback == nil {
		// if the handler is nil, use srv handler
		callback = srv.callback
	}
	c := NewTCPConn(conn, callback, protocol)
	if srv.readDeadline > 0 {
		log.Println(c.setReadDeadline(srv.readDeadline))
	}
	c.Serve()
	return c
}

//Connect 使用指定的callback和protocol连接其他TCPServer，返回TCPConn
func (srv *TCPServer) Connect(ip string, callback CallBack, protocol Protocol) (*TCPConn, error) {
	tcpAddr, err := net.ResolveTCPAddr("tcp", ip)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialTCP("tcp", nil, tcpAddr)
	if err != nil {
		return nil, err
	}

	tcpConn := srv.newTCPConn(conn, callback, protocol)
	return tcpConn, nil

}

//Close 首先关闭所有连接，然后关闭TCPServer
func (srv *TCPServer) Close() {
	defer srv.listener.Close()
	for _, c := range srv.bucket.GetAll() {
		if !c.IsClosed() {
			c.Close()
		}
	}
}

func (srv *TCPServer) removeClosedTCPConn() {
	select {
	case <-srv.exitChan:
		return
	default:
		removeKey := make(map[string]struct{})
		for key, conn := range srv.bucket.GetAll() {
			if conn.IsClosed() {
				removeKey[key] = struct{}{}
			}
		}
		for key := range removeKey {
			srv.bucket.Delete(key)
		}
	}
}

//GetAllTCPConn 返回所有客户端连接
func (srv *TCPServer) GetAllTCPConn() []*TCPConn {
	conns := []*TCPConn{}
	for _, conn := range srv.bucket.GetAll() {
		conns = append(conns, conn)
	}
	return conns
}

func (srv *TCPServer) GetTCPConn(key string) *TCPConn {
	return srv.bucket.Get(key)
}

func (srv *TCPServer) SetReadDeadline(t time.Duration) {
	srv.readDeadline = t
}
