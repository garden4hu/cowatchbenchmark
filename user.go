package cowatchbenchmark

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

func newUser() *User {
	return &User{name: generateUserName(4), uid: getHostId(), hostCoWatch: false, connected: false, readyForMsg: false}
}

// UsersConnection try to connect to the server and exchange message.
// param when is the time for requesting of websocket concurrently
// param mode is the mode for requesting. 0 means parallel and 1 means serial
func (p *RoomUnit) UsersConnection(start chan struct{}, ctx context.Context, wg *sync.WaitGroup) {
	if p.roomManager.ParallelRequest {
		defer wg.Done()
	}
	// create Users
	var wg2 sync.WaitGroup // 用于并发请求，确保所有的goroutine 同时发起请求，而不会出现开始并发请求时，有的goroutine 还没有构造好 ws 句柄
	for i := 0; i < p.usersCap; i++ {
		wg2.Add(1)
		u := func() *User {
			if i == 0 {
				return p.Users[0]
			} else {
				return newUser()
			}
		}()
		go u.joinRoom(p, p.roomManager.ParallelRequest, ctx, start, &wg2)
	}
	wg2.Wait()
	if p.roomManager.ParallelRequest == false {
		time.Sleep(10 * time.Millisecond)
	}
}

// User join the room on the websocket server
func (p *User) joinRoom(r *RoomUnit, parallel bool, ctx context.Context, start chan struct{}, wg *sync.WaitGroup) {
	wsHandler := func() (*websocket.Conn, error) {
		// set ws/wss url param
		v := url.Values{}
		v.Add("uid", strconv.Itoa(p.uid))
		v.Add("name", p.name)
		v.Add("version", r.SdkVersion)
		v.Add("roomId", r.roomName)
		v.Add("EIO", "3")
		v.Add("transport", "websocket")
		u := url.URL{Host: r.Address, Path: "/socket.io/", ForceQuery: true, RawQuery: v.Encode()}
		switch r.Schema {
		case "http":
			u.Scheme = "ws"
			break
		case "https":
			u.Scheme = "wss"
			break
		default:
			u.Scheme = "wss"
			break
		}
		dialer := &websocket.Dialer{
			Proxy:             http.ProxyFromEnvironment,
			HandshakeTimeout:  r.wsTimeout,
			EnableCompression: true,
		}
		// set http->websocket header
		rq := http.Header{}
		rq.Add("Accept-Encoding", "gzip, deflate, br")
		rq.Add("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8,en-GB;q=0.7,en-US;q=0.6")
		rq.Add("Cache-Control", "no-cache")
		// rq.Add("Connection","Upgrade")
		rq.Add("Pragma", "no-cache")
		rq.Add("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/88.0.4324.96 Safari/537.36 Edg/88.0.705.56")
		//rq.Add("Sec-WebSocket-Extensions","permessage-deflate; client_max_window_bits") // enable compress
		var conn *websocket.Conn
		var err error = nil
		for i := 0; i < 3; i++ {
			conn, _, err = dialer.Dial(u.String(), rq)
			if err != nil {
				if i < 3 {
					time.Sleep(50 * time.Millisecond)
					continue
				}
			} else {
				break
			}
		}
		if err != nil {
			log.Println("failed to dial websocket:", err)
			return nil, err
		}
		return conn, nil
	}
	// if Request users concurrently, goroutine should be waited
	wg.Done() // create goroutine done
	if parallel {
		<-start // waiting for starting
	}
	startJoin := time.Now()
	conn, err := wsHandler()
	if err != nil {
		log.Println("failed to create websocket connection, err = ", err)
		return
	}
	// add user to RoomUnit
	r.muxUsers.Lock()
	r.Users = append(r.Users, p) // add user to room
	r.muxUsers.Unlock()
	r.roomManager.notifyUserAdd <- 1 // 通知新增用户

	p.ConnectionDuration = time.Since(startJoin)
	defer conn.Close()
	p.connected = true

	defer func() {
		r.roomManager.notifyUserAdd <- -1
	}() // 通知用户下线

	done := make(chan bool)
	defer close(done)
	// starting a new goroutine for processMessage the websocket message
	go processMessage(conn, r, p, done, ctx)

	pingTicker := time.NewTicker(time.Millisecond * time.Duration(r.PingInterval))
	log.Println("ping ticker duration:", r.PingInterval)
	defer pingTicker.Stop()
	// 对于测试环境而言，host 发送的 sync 信息频次较高，故对于 host User，需要考虑其发送频率
	// 而对于 Guests, 其 websocket 消息内容更多为 text，ping/pong，这些消息频次较低
	sendMsgTicker := time.NewTicker(r.msgSendingInternal)
	defer sendMsgTicker.Stop()
	log.Println("sending MSG  ticker duration:", r.msgSendingInternal.String())
	for {
		select {
		case <-done:
			// need to reconnect
			conn.Close()
			conn, err = wsHandler()
			if err != nil {
				log.Println("failed to reconnect to ws server, err = ", err)
				return
			} else {
				go processMessage(conn, r, p, done, ctx)
			}
			break
		case _ = <-pingTicker.C:
			// reset pingTicker and send ping
			if conn != nil {
				p.Lw.Lock()
				err := conn.WriteMessage(websocket.TextMessage, []byte("2"))
				p.Lw.Unlock()
				if err != nil {
					log.Println("write:", err)
				}
			}
			pingTicker.Reset(time.Millisecond * time.Duration(r.PingInterval))
			// sending msg
		case _ = <-sendMsgTicker.C:
			if p.hostCoWatch {
				// 在测试环境中，由于用户的 text 的信息数量可以忽略，故此处只允许 host 发送消息到服务器
				if p.readyForMsg {
					msg := generateMessage(r)
					p.Lw.Lock()
					if conn != nil {
						_ = conn.WriteMessage(websocket.TextMessage, msg)
					}
					p.Lw.Unlock()
					sendMsgTicker.Reset(r.msgSendingInternal)
				}
			}
		case <-ctx.Done():
			return
		}
	}
}
