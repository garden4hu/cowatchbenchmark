package cowatchbenchmark

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// NewRoom return a RoomUnit object
func NewRoom(host string, httpTimeout, wsTimeout time.Duration, maximumUsers, msgLength, frequency int, appId string, rm *RoomManager) *RoomUnit {
	if frequency == 0 {
		frequency = 10
	}
	room := &RoomUnit{usersCap: maximumUsers, msgLength: msgLength, msgSendingInternal: time.Microsecond * time.Duration(60*1000*1000/frequency), AppId: appId, roomManager: rm}
	ur, _ := url.Parse(host)
	room.Schema = ur.Scheme
	room.Address = ur.Host
	// set initial ping interval
	room.PingInterval = 25000
	room.httpTimeout = httpTimeout
	room.wsTimeout = wsTimeout
	room.ExpireTime = 1440
	room.SdkVersion = "1.0.0-7295-integration-b2a92020"
	room.condMutex = &sync.Mutex{}
	room.cond = sync.NewCond(room.condMutex)
	return room
}

func (p *RoomUnit) Request() error {
	strings.TrimSuffix(p.Address, "/")
	uri := p.Schema + "://" + p.Address + "/" + "createRoom"
	tr := func() *http.Transport {
		return &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}
	newClient := func() *http.Client {
		if p.Schema == "https" {
			return &http.Client{Transport: tr(), Timeout: p.httpTimeout}
		} else {
			return &http.Client{Timeout: p.httpTimeout}
		}

	}()
	// p.preRequest()
	start := time.Now()
	// construct body
	roomId := getHostId()
	body := generateCreatingRoomReqBody(CreateRoomReqBody{HostUid: roomId, AppID: p.AppId, ExpireTime: p.ExpireTime, Version: p.SdkVersion})
	req, _ := http.NewRequest("POST", uri, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("content-length", fmt.Sprintf("%d", len(body)))
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/90.0.4430.93 Safari/537.36 Edg/90.0.818.56")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("accept", "*/*")
	req.Header.Set("origin", "https://cowatch.visualon.cn:8080")
	req.Header.Set("referer", "https://cowatch.visualon.cn:8080/")
	req.Header.Set("sec-fetch-dest", "empty")
	req.Header.Set("sec-fetch-mode", "cors")
	req.Header.Set("sec-fetch-site", "same-site")
	req.Header.Set("sec-ch-ua", "\" Not A;Brand\";v=\"99\", \"Chromium\";v=\"90\", \"Microsoft Edge\";v=\"90\"")
	req.Header.Set("sec-ch-ua-mobile", "?0")
	req.Header.Set("dnt", "1")

	resp, err := newClient.Do(req)
	if err != nil {
		fmt.Println("Failed to post, err = ", err)
		return errors.New("failed to post")
	}
	defer resp.Body.Close()
	p.ConnectionDuration = time.Since(start)
	roomRaw, _ := ioutil.ReadAll(resp.Body)

	// unmarshal
	room := new(Room)
	err = json.Unmarshal(roomRaw, room)
	if err != nil {
		fmt.Println("createRoom 返回 json 解析失败")
		return errors.New("createRoom 返回 json 解析失败")
	}
	p.roomName = room.Name
	p.RoomId = roomId

	// Note: 房间创建完成后，即产生第一个 User， 也是 Host
	p.Users = append(p.Users, &User{name: generateUserName(4), hostCoWatch: true, uid: roomId, connected: false, readyForMsg: false})

	// add room to roomManager
	p.roomManager.LckRoom.Lock()
	p.roomManager.Rooms = append(p.roomManager.Rooms, p)
	p.roomManager.LckRoom.Unlock()
	return nil
}

type CreateRoomReqBody struct {
	AppID      string `json:"appId"`
	ExpireTime int    `json:"expireTime"`
	HostUid    int    `json:"hostUid"`
	Version    string `json:"version"`
}

// return a string of json
func generateCreatingRoomReqBody(body CreateRoomReqBody) string {
	str, _ := json.Marshal(body)
	return string(str)
}

// for some version, it maybe has options method to request
func (p *RoomUnit) preRequest() {
	strings.TrimSuffix(p.Address, "/")
	uri := p.Schema + "://" + p.Address + "/" + "createRoom"
	tr := func() *http.Transport {
		return &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}
	newClient := func() *http.Client {
		if p.Schema == "https" {
			return &http.Client{Transport: tr(), Timeout: p.httpTimeout}
		} else {
			return &http.Client{Timeout: p.httpTimeout}
		}

	}()
	// request options method
	preReq, _ := http.NewRequest("OPTIONS", uri, nil)
	preReq.Header.Set("access-control-request-headers", "content-type")
	preReq.Header.Set("access-control-request-method", "POST")
	preReq.Header.Set("accept", "*/*")
	preReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/90.0.4430.93 Safari/537.36 Edg/90.0.818.56")
	preReq.Header.Set("Accept-Encoding", "gzip, deflate, br")
	preReq.Header.Set("origin", "https://cowatch.visualon.cn:8080")
	preReq.Header.Set("referer", "https://cowatch.visualon.cn:8080/")
	preReq.Header.Set("sec-fetch-dest", "empty")
	preReq.Header.Set("sec-fetch-mode", "cors")
	preReq.Header.Set("sec-fetch-site", "same-site")
	_, err := newClient.Do(preReq)
	if err != nil {
		fmt.Println("Failed to send OPTIONS method, err = ", err)
		// return fmt.Errorf("failed to send OPTIONS method, err:%s", err.Error())
	}
	// 如果 err != nil 则不能 close body，此处可以省略
}
