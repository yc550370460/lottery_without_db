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
	"strings"
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
// HeartbeatGap
const HeartbeatGap = 60

var pool WebsocketPool

var Winner = make(map[string]bool)
var Exclude = make(map[string]bool)

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
		fmt.Println("Reach the max and will pop the previous conn")
		cr.util = cr.util[1:]
		IdTmp , errUuid := uuid.NewV4()
		if errUuid != nil{
			panic("Generate uuid failed")
			return false
		}
		cr.util = append(cr.util, WebsocketUtil{IdTmp, ws, true, activeTime})
	}else{
		IdTmp , errUuid := uuid.NewV4()
		if errUuid != nil{
			panic("Generate uuid failed")
			return false
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

func (cr *WebsocketPool)UpdateWebsocketActivetime(id uuid.UUID, activetime int64){
	cr.Lock()
	defer cr.Unlock()
	var tmp int
	for index, value :=range cr.util{
		if value.connId == id{
			tmp = index
		}
	}
	cr.util[tmp].activeTime = activetime
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
	cr.Lock()
	defer cr.Unlock()

	var tmp int = -1
	for index, value :=range cr.util{
		if value.connId == id{
			tmp = index
		}
	}
	if tmp > -1{
		tmpUtil := cr.util[tmp+1:]
		cr.util = cr.util[:tmp]
		for i,_ := range tmpUtil[:]{
			cr.util = append(cr.util, tmpUtil[i])
		}
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

}

var staticReg = regexp.MustCompile("static")
var indexReg = regexp.MustCompile("index")
type AwardOutput struct{
	Employee settings.EmployeeList
	Winner []string}

type SaveLotteryOutput struct{
	Status string
	Winner string}

func award(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		var winnerList []string
		for key, value := range Winner{
			if value{
				winnerList = append(winnerList, key)
			}
		}
		t, _ := template.ParseFiles(templatePath + "/award.gtpl")
		t.Execute(w, AwardOutput{Employees, winnerList})
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
		if errSend := websocket.Message.Send(ws, "status:connect failed, please try again later");errSend != nil{
			log.Println(errSend)
			log.Println("Client connect error")
			return
		}
	}

	wsRecv :=pool.FindWebsocketByConn(ws)
	log.Println("New connection, conn id is:",wsRecv.connId)
	log.Println(pool.util)

	defer func() {
		log.Println("Error happend and disconnect")
		pool.ReleaseWebsocketConn(wsRecv.connId)
	}()

	for {
		START:
		var reply string
		if errRecv := websocket.Message.Receive(ws, &reply);errRecv != nil{
			tmp :=pool.FindWebsocketByConn(ws)
			log.Println(errRecv)
			log.Println("Client disconnect for conn:", tmp.connId)
			return
		}

		log.Println(pool.util)

		pool.UpdateWebsocketActivetime(wsRecv.connId, time.Now().Unix())

		log.Println("current conn is:",&wsRecv.conn)
		for _, _ws := range pool.util {
			if time.Now().Unix() - _ws.activeTime > HeartbeatGap{
				log.Println("Timeout point")
				log.Println("Timeout for client:", _ws.connId)
				if _ws.conn == ws{
					return
				}else{
					pool.ReleaseWebsocketConn(_ws.connId)
				}
			}
		}

		bubbleLength := len(msgPool.Message)

		if bubbleLength < 1{
			time.Sleep(10*time.Second)
			goto START
		}

		data, errGetData := msgPool.Pop()
		if errGetData != nil{
			log.Println(errGetData)
			panic("Get bubble data Error")
		}

		for _, _ws := range pool.util {
			if time.Now().Unix() - _ws.activeTime < HeartbeatGap{
				if err := websocket.Message.Send(_ws.conn, string(data)); err != nil {
					log.Println(err)
					log.Println("Connect error for client:", _ws.connId)
					if _ws.conn == ws{
						return
					}else{
						pool.ReleaseWebsocketConn(_ws.connId)
					}
				}
			}else{
				log.Println("Timeout for client:", _ws.connId)
				if _ws.conn == ws{
					return
				}else{
					pool.ReleaseWebsocketConn(_ws.connId)
				}
			}
		}
	}
}


func SetTimer(){
	time.Sleep(3* time.Second)
}

func saveLottery(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	var dataList []string
	t := template.New("saveLottery")
	if r.Method == "GET" {
		tmpList, ok := r.URL.Query()["list"]
		if !ok || len(tmpList) < 1{
			t.Execute(w, SaveLotteryOutput{"failed", ""})
		}else{
			dataList = strings.Split(tmpList[0], ",")
		}
		if len(dataList) > 0{
			for _, value :=range dataList{
				if value != ""{
					fmt.Println(value)
					Winner[value] = true
				}
			}
			var winnerList []string
			var winnerString string
			for key, value := range Winner{
				if value{
					winnerList = append(winnerList, key)
				}
			}
			winnerString = strings.Join(winnerList, ",")
			fmt.Println(SaveLotteryOutput{"success", winnerString})
			t.Parse("{{.Status}}:" + "{{.Winner}}")
			t.Execute(w, SaveLotteryOutput{"success", winnerString})
		}else{
			//fmt.Fprintf(w, "failed")
			t.Execute(w, SaveLotteryOutput{"failed", ""})
		}
	}else{
		fmt.Println("method is:" + r.Method)
		//fmt.Fprintf(w, "Method illegal")
		t.Execute(w, SaveLotteryOutput{"failed", ""})
	}
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

	// award page, just show the page
	http.HandleFunc("/save_lottery/", saveLottery)

	// login page
	http.HandleFunc("/message/", message)

	// get the bubble text and send to award page
	http.Handle("/bubble", websocket.Handler(bubble))

	http.ListenAndServe(":1234", nil)
	if err := http.ListenAndServe(":1234", nil); err != nil {
		log.Fatal("ListenAndServe:", err)
	}
}
