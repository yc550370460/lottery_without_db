package main

import (
	"./settings/jsonParse"
	"fmt"
	"github.com/satori/go.uuid"
	"golang.org/x/net/websocket"
	"html/template"
	"log"
	"net/http"
	"regexp"
	"sync"
	"time"
)

// static path
var staticPath string
// template path
var templatePath string
// define Employee struct instance
var Employees settings.EmployeeList
// define redis interface instance
//var redisClient redis.RedisR
// max messages for redis save
const MaxLengthBubble int = 100
// max count for websocket conn
const WebsocketMax = 10
// error code for apply websocket conn apply
const GetWebsocketFailed = -1
// message queue max length
const QueueMax = 100
// message max length
const MessageMax = 100
// init MessagePool
var msgPool MessagePool

var pool WebsocketPool

type MessagePool struct{
	sync.Mutex
	Message []string
}

type MessagePoolFullError string
type MessageLengthError string
type MessagePoolEmptyError string

func (e MessagePoolFullError) Error() string   { return "message pool full for message: " + string(e) }
func (e MessagePoolEmptyError) Error() string   { return "message pool empty for pop, length is:" + string(e)}

func (msg *MessagePool) Push(message string) (err error){
	msg.Lock()
	defer msg.Unlock()
	lenTmp := len(msg.Message)
	if lenTmp < QueueMax{
		msg.Message = append(msg.Message, message)
		return nil
	}else{
		return MessagePoolFullError(message)
	}
}

func (msg *MessagePool) Pop() (string, error){
	msg.Lock()
	defer msg.Unlock()
	lenTmp := len(msg.Message)
	if lenTmp > 0{
		fmt.Println(lenTmp)
		msgTmp := msg.Message[lenTmp - 1]
		msg.Message = msg.Message[0: lenTmp - 1]
		return msgTmp, nil
	}else{
		return "", MessagePoolEmptyError(lenTmp)
	}
}

type WebsocketUtil struct {
	connId uuid.UUID
	conn *websocket.Conn
	status bool
	activeTime int64
}

type WebsocketPool struct {
	sync.Mutex
	util []WebsocketUtil
	poolId uuid.UUID
}

func (cr *WebsocketPool)GetWebsocketConn(ws *websocket.Conn) bool{
	cr.Lock()
	activeTime := time.Now().Unix()
	defer cr.Unlock()
	if len(cr.util) >= WebsocketMax{
		fmt.Println("Reach the max and  will pop the previous conn")
		cr.util = cr.util[1:]
		IdTmp , errUuid := uuid.NewV4()
		if errUuid != nil{
			panic("Generate uuid failed")
		}
		cr.util = append(cr.util, WebsocketUtil{IdTmp, ws, true, activeTime})
	}else{
		IdTmp , errUuid := uuid.NewV4()
		if errUuid != nil{
			panic("Generate uuid failed")
		}
		cr.util = append(cr.util, WebsocketUtil{IdTmp, ws, true, activeTime})
	}
	return true
}

func (cr *WebsocketPool)FindWebsocketByConn(ws *websocket.Conn) (conn *WebsocketUtil){
	cr.Lock()
	defer cr.Unlock()

	var tmp int
	for index, value :=range cr.util{
		if value.conn == ws{
			tmp = index
		}
	}
	return &cr.util[tmp]
}


func (cr *WebsocketPool)UpdateWebsocketConnStatus(id uuid.UUID, status bool) {
	cr.Lock()
	defer cr.Unlock()
	var tmp int
	for index, value :=range cr.util{
		if value.connId == id{
			tmp = index
		}
	}
	cr.util[tmp].status = status
}

func (cr *WebsocketPool)ReleaseWebsocketConn(id uuid.UUID){
	//if _, isPresent := cr.util[id];isPresent{
	//	delete(cr.util, id)
	//}
	cr.Lock()
	defer cr.Unlock()

	var tmp int
	for index, value :=range cr.util{
		if value.connId == id{
			tmp = index
		}
	}
	for i,_ := range cr.util[tmp+1:]{
		cr.util = append(cr.util[:tmp], cr.util[i])
	}
}

func (cr *WebsocketPool)DeletePool(){
	pool = WebsocketPool{}
}


func init(){
	var Config settings.Config
	var ConfigData settings.JsonParse = &Config

	var Employee settings.EmployeeList
	var EmployeeData settings.JsonParse = &Employee

	// parse config.json
	errConfig := ConfigData.Load("settings/config.json")
	if errConfig != nil {
		panic("Parse config json failed")
	}
	staticPath = Config.Static
	templatePath = Config.Template

	// parse employee.json
	employeeConfig := EmployeeData.Load("static/employee.json")
	if employeeConfig != nil {
		panic("Parse config json failed")
	}
	Employees = Employee

	//redisClient = &redis.GlobalRedisClient
}



//func bubbleTest(ws *websocket.Conn) {
//	var err error
//	for {
//		var reply string
//
//		if err = websocket.Message.Receive(ws, &reply); err != nil {
//			fmt.Println(err)
//			fmt.Println("Client lost")
//			return
//		}
//
//		RedisBubbleLength, errLength := redisClient.RedisLLen("bubble")
//		if errLength != nil {
//			panic(errLength)
//		}
//		for RedisBubbleLength > MaxLengthBubble {
//			_, errGetData := redisClient.RedisLpop("bubble")
//			if errGetData != nil {
//				panic(errGetData)
//			}
//			RedisBubbleLength -= 1
//
//		}
//		errPush := redisClient.RedisRpush("bubble", reply)
//		if errPush != nil {
//			panic(errPush)
//		} else {
//			errStatus := redisClient.RedisSet("bubbleStatus", "true")
//			if errStatus != nil {
//				panic(errStatus)
//
//			}
//			msg := reply
//			if err = websocket.Message.Send(ws, msg); err != nil {
//				panic(err)
//			}
//		}
//	}
//}

var staticReg = regexp.MustCompile("static")
var indexReg = regexp.MustCompile("index")


func award(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		t, _ := template.ParseFiles(templatePath + "/award.gtpl")
		t.Execute(w, Employees)
	}
}

func message(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	if r.Method == "GET" {
		t, _ := template.ParseFiles(templatePath + "/message.gtpl")
		t.Execute(w, nil)
	}else if r.Method == "POST"{
        tmpMessage :=r.Form["message"][0]
        if len(tmpMessage) > MessageMax{
			fmt.Println("Message length is not less than " + string(MessageMax))
			t, _ := template.ParseFiles(templatePath + "/message.gtpl")
			t.Execute(w, nil)
			return
		}
		msgPushError := msgPool.Push(tmpMessage)
		fmt.Println(msgPool.Message)
		if msgPushError != nil{
			fmt.Println(msgPushError)
		}
		t, _ := template.ParseFiles(templatePath + "/message.gtpl")
		t.Execute(w, nil)
	}else{
		fmt.Println("method is:" + r.Method)
		fmt.Fprintf(w, "Method illegal")
		return
	}
}


func bubble(ws *websocket.Conn) {
	if status := pool.GetWebsocketConn(ws); status == false{
		if errSend := websocket.Message.Send(ws, "status:获取连接失败");errSend != nil{
			fmt.Println(errSend)
			panic("Send msg to web socket Error, Get new conn phase")
		}
		return
	}

	for {
		START:
		wsRecv :=pool.FindWebsocketByConn(ws)
		wsRecv.activeTime = time.Now().Unix()

		time.Sleep(1*time.Second)
		bubbleLength := len(msgPool.Message)
		if bubbleLength < 1{
			time.Sleep(10*time.Second)
			goto START
		}

		data, errGetData := msgPool.Pop()
		if errGetData != nil{
			fmt.Println(errGetData)
			panic("Get bubble data Error")
		}

		for _, _ws := range pool.util {
			if time.Now().Unix() - _ws.activeTime < 10{
				fmt.Println(pool.util)
				if err := websocket.Message.Send(_ws.conn, string(data)); err != nil {
					fmt.Println(err)
					fmt.Println(_ws.connId)
					pool.ReleaseWebsocketConn(_ws.connId)
				}
			}else{
				pool.ReleaseWebsocketConn(_ws.connId)
			}
		}
	}
}


func SetTimer(){
	time.Sleep(3* time.Second)
}


func main() {
	var errUUID error
	pool = WebsocketPool{}
	pool.poolId , errUUID= uuid.NewV4()
	if errUUID != nil{
		panic("Apply Pool failed")
	}

	// static files url
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(staticPath))))

	// award page, just show the page
	http.HandleFunc("/lottery/", award)

	// login page
	http.HandleFunc("/message/", message)

	// Get the bubble text from user and store in redis
	//http.Handle("/web_socket", websocket.Handler(bubbleTest))
	// get the bubble text and send to award page
	http.Handle("/bubble", websocket.Handler(bubble))

	http.ListenAndServe(":1234", nil)
	if err := http.ListenAndServe(":1234", nil); err != nil {
		log.Fatal("ListenAndServe:", err)
	}
}
