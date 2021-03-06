package util

import (
	"bytes"
	"fmt"
	"io/ioutil"
	. "jus"
	. "jus/str"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"
)

type urlMap struct {
	pattern string
	path    string
	cls     int
}

type proxyMap struct {
	pattern *url.URL
	path    string
	cls     int
}
type element struct {
	name       string
	info       os.FileInfo       //文件信息
	comment    string            //所有文字描述
	attributes map[string]string //分析带@的属性
	cls        int               //判断是JS类文件还是HTML文件，0：html，1:js
}

type connectElement struct {
	Time       int64
	Connected  bool
	Conn       *websocket.Conn
	Name       string
	IP_Address string
	RemoteAddr string
	LocalAddr  string
}

type WsUser struct {
	sync.RWMutex
	list map[string]*connectElement
}

type JusServer struct {
	protocol      string //连接协议http or https
	Addr          string //连接地址
	Status        bool   //运行状态
	Datetime      time.Time
	server        *http.Server
	fServer       http.Handler
	osName        string //操作系统名称
	SysPath       string
	RootPath      string
	jusDirName    string
	proxy         []*proxyMap        //反向代理列表
	pattern       map[string]*urlMap //映射列表
	useClassList  []*element
	wsUser        *WsUser
	connectedList []*connectElement
	testConnect   chan byte
	wsURL         string //websocket 用户验证URL
}

/**
 * @param SysPath	系统类路径
 * @param rootPath	工程类路径
 * 启动函数
 */
func (u *JusServer) CreateServer(SysPath string, rootPath string) {
	u.Datetime = time.Now()
	u.SysPath = SysPath
	if rootPath != "" {
		u.SetProject(rootPath)
	}

	u.jusDirName = "/juis/"
	u.proxy = make([]*proxyMap, 0)
	u.pattern = make(map[string]*urlMap, 0)
	u.wsUser = &WsUser{list: make(map[string]*connectElement)} //初始化
	u.connectedList = make([]*connectElement, 0)
	u.testConnect = make(chan byte)
}

/**
 * 服务器监测
 */
func (u *JusServer) testServer() {
	u.testConnect = make(chan byte)
	go func() {
		for {
			_, err := <-u.testConnect
			if !err {
				break
			}
			newList := make([]*connectElement, 0, len(u.connectedList))
			for _, v := range u.connectedList {
				if !v.Connected && (time.Now().Unix()-v.Time > 5) { //未连接并且大于5秒
					v.Conn.Close()
				} else {
					newList = append(newList, v)
				}
			}
			u.connectedList = newList
		}

	}()

	go func() {
		for u.Status {
			time.Sleep(5 * time.Second)
			u.testConnect <- 1
		}
		close(u.testConnect)

	}()
}

/**
 * 获取当前Websocket用户的服务器列表
 */
func (u *JusServer) WebsocketList() []*connectElement {
	return u.connectedList
}

func (u *JusServer) Start(addr string) {
	if u.Status {
		fmt.Println("服务已经开启.")
		return
	}
	if Index(addr, "http://") == 0 {
		u.protocol = "http"
		addr = Substring(addr, len("http://"), -1)
	} else if Index(addr, "https://") == 0 {
		u.protocol = "https"
		addr = Substring(addr, len("https://"), -1)
	}
	u.Addr = addr
	go func() {
		fmt.Println("JUS Server Started At: [" + addr + "]. Use protocol " + IfStr(u.protocol == "", "http", u.protocol))
		handler := http.NewServeMux()
		handler.HandleFunc("/", u.root)
		handler.HandleFunc("/index.edit/", u.editDirEvt)
		handler.HandleFunc("/index.edit/juis/", u.jusEditEvt)
		handler.Handle("/ws", websocket.Handler(u.wsHandler))
		u.server = &http.Server{Addr: addr, Handler: handler}
		u.Status = true
		u.testServer()
		var err error = nil
		if u.protocol == "" || u.protocol == "http" {
			err = u.server.ListenAndServe()
		} else if u.protocol == "https" {
			err = u.server.ListenAndServeTLS(u.RootPath+"/ssl/cert.pem", u.RootPath+"/ssl/key.pem")
		}

		if err != nil {
			fmt.Println("status:", err)
		}
		u.Status = false
		fmt.Println("JUS Server END.")

	}()

}

/**
 * 设置工程目录
 */
func (u *JusServer) SetProject(path string) bool {
	if Exist(path) {
		rpath, _ := filepath.Abs(path)
		u.RootPath = rpath
		u.fServer = http.FileServer(http.Dir(path))
		u.proxy = u.proxy[0:0]
		for _, v := range u.GetAttrLike("proxy") {
			u.AddDomainProxy(v[0], v[1])
		}
		u.pattern = make(map[string]*urlMap)
		for _, v := range u.GetAttrLike("pattern") {
			u.AddProxy(v[0], v[1])
		}
		for _, v := range u.GetAttrLike("ws_accept") { //添加websocket用户验证url
			fmt.Println("ws_accept", v[0])
			u.wsURL = v[0]
		}
		return true
	} else {
		fmt.Println("不存在[" + path + "]目录")
		return false
	}

}

/**
 * 创建模块文件
 */
func (u *JusServer) CreateModule(cls string, className string) bool {
	tPath := "" //临时路径
	path := u.RootPath + "/code/" + Replace(className, ".", "/")
	dirPath := Substring(path, 0, LastIndex(path, "/"))

	if !Exist(dirPath) {
		os.MkdirAll(dirPath, 0777)
	}

	if Index(cls, "s") != -1 { //创建Script文件
		tPath = path + ".js"
		fmt.Println("Module Path: ", tPath)
		if !Exist(tPath) {
			f, e := os.Create(tPath)
			if e == nil {
				defer f.Close()
			}
		}

	}

	if Index(cls, "m") != -1 { //创建多个文件，包括*.html,*.js,*.css
		tPath = path + ".html"
		fmt.Println("Module Path: ", tPath)
		if !Exist(tPath) {
			f, e := os.Create(tPath)
			if e == nil {
				defer f.Close()
			}
		}
		tPath = path + ".js"
		fmt.Println("Module Path: ", tPath)
		if !Exist(tPath) {
			f, e := os.Create(tPath)
			if e == nil {
				defer f.Close()
			}
		}
		tPath = path + ".css"
		fmt.Println("Module Path: ", tPath)
		if !Exist(tPath) {
			f, e := os.Create(tPath)
			if e == nil {
				defer f.Close()
			}
		}

	}

	if Index(cls, "h") != -1 { //默认创建HTML文件
		tPath = path + ".html"
		fmt.Println("Module Path: ", tPath)
		if !Exist(tPath) {
			f, e := os.Create(tPath)
			if e == nil {
				defer f.Close()
			}
		}
	}

	if Index(cls, "r") != -1 { //默认创建资源文件夹
		os.MkdirAll(path+".RES", 0777)
		fmt.Println("Module RES: ", path)
	}

	return true
}

/**
 * 关闭本次服务
 */
func (u *JusServer) Close() error {
	u.Status = false
	if u.server != nil {
		return u.server.Close()
	}
	for _, v := range u.connectedList {
		v.Conn.Close()
	}
	u.connectedList = make([]*connectElement, 0)
	return nil
}

/**
 *
 */
func (u *JusServer) wsHandler(ws *websocket.Conn) {
	ce := &connectElement{Time: time.Now().Unix(), Connected: false, Conn: ws}
	u.connectedList = append(u.connectedList, ce)
	msg := make([]byte, 256) //8 8 4 4 2 ...
	n, err := ws.Read(msg)
	var cmds []string
	if err != nil {
		fmt.Println("error>>:", err)
	} else {
		cmds = FmtCmd(string(msg[0:n]))
		if len(cmds) >= 3 {
			if cmds[0] == "login" {
				if flag, value := u.havUser(cmds); flag {
					u.wsUser.RLock()
					if u.wsUser.list[cmds[1]] != nil {
						u.wsUser.list[cmds[1]].Conn.Write([]byte("close"))
						u.wsUser.list[cmds[1]].Conn.Close()
					}
					u.wsUser.RUnlock()
					ce.Connected = true
					ce.Name = cmds[1]
					ce.IP_Address = ws.Request().RemoteAddr
					ce.RemoteAddr = ws.RemoteAddr().String()
					ce.LocalAddr = ws.LocalAddr().String()
					u.wsUser.Lock()
					u.wsUser.list[cmds[1]] = ce
					u.wsUser.Unlock()
					fmt.Println(cmds[1] + " Login.")
					ws.Write([]byte(value))
					for {
						n, err = ws.Read(msg)
						if err != nil {
							break
						}
						fmt.Println("read:", string(msg[0:n]))
						pkg := Package{from: cmds[1], data: msg[0:n]}
						fmt.Println(pkg.router(), pkg.uuid(), pkg.frame(), pkg.value())
						u.wsUser.RLock()
						pkg.ToUser(u.wsUser.list)
						u.wsUser.RUnlock()
					}
				} else {
					ws.Write([]byte(value))
				}
			} else {
				fmt.Println("未识别请求")
			}
		}

	}
	ce.Connected = false
	fmt.Println("连接被断开")
	u.testConnect <- 1

}

/**
 * 服务器下发信息
 */
func (u *JusServer) Send(router string, uuid string, value string) {
	buff := bytes.NewBufferString(router)
	buff.WriteByte(0)
	buff.WriteString(uuid)
	buff.WriteByte(0)
	buff.WriteString("-")
	buff.WriteByte(0)
	buff.WriteString(value)
	u.wsUser.RLock()
	pkg := &Package{from: "God", data: buff.Bytes()}
	pkg.ToUser(u.wsUser.list)
	u.wsUser.RUnlock()
}

/**
 * 判断是否存在此用户
 */
func (u *JusServer) havUser(cmds []string) (bool, string) {
	if u.wsURL != "" {
		data := make(url.Values)
		data["name"] = []string{cmds[1]}
		data["pass"] = []string{cmds[2]}
		res, err := http.PostForm(u.wsURL, data)
		if err != nil {
			return false, err.Error()
		}
		dat, e := ioutil.ReadAll(res.Body)
		str := string(dat)
		if e != nil {
			return false, e.Error()
		}
		if StringLen(str) > 6 {
			if Substring(str, 0, 7) == "accept " {
				return true, str
			} else {
				return false, str
			}
		} else {
			return false, ""
		}

	} else {
		return true, "accept "
	}

}

func (u *JusServer) jusEvt(w http.ResponseWriter, req *http.Request) {
	path := u.RootPath + req.RequestURI
	if Exist(path) {
		value, err := GetBytes(path)
		if err != nil {
			value = []byte("500")
		}
		w.Write(value)
	} else {
		jus := &JUS{SYSTEM_PATH: u.SysPath, CLASS_PATH: u.SysPath + "/code/"}
		className := Substring(req.RequestURI, StringLen(u.jusDirName), LastIndex(req.RequestURI, "."))
		className = Replace(className, "/", ".")
		if jus.CreateFrom(u.RootPath+"/code/", "", nil, className) {
			jus.resPath = "code"
			b := jus.ToFormatBytes()
			w.Header().Add("Content-Length", strconv.Itoa(len(b)))
			w.Write(b)
		} else {
			fmt.Println("不存在", className)
			w.WriteHeader(404)
			w.Write([]byte("<h1>404</h1>"))
		}
		jus = nil

	}

}

func (u *JusServer) jusEditEvt(w http.ResponseWriter, req *http.Request) {

	path := u.SysPath + req.RequestURI
	if Exist(path) {
		u.root(w, req)
	} else {
		jus := &JUS{SYSTEM_PATH: u.SysPath, CLASS_PATH: u.SysPath + "/code/"}
		className := Substring(req.RequestURI, StringLen("index.edit/"+u.jusDirName), LastIndex(req.RequestURI, "."))
		if jus.CreateFrom(u.SysPath+"/code/", "", nil, className) {
			jus.resPath = "code"
			b := jus.ToFormatBytes()
			w.Header().Add("Content-Length", strconv.Itoa(len(b)))
			w.Write(b)
		} else {
			fmt.Println("不存在", className)
		}

	}

}

func (u *JusServer) root(w http.ResponseWriter, req *http.Request) {
	//判断是否有域名反向代理
	if u.hasProxy(w, req) {
		return
	}
	if req.URL.Path == "/" {

	} else {
		if Index(req.URL.Path, "/juis/") == 0 {
			u.jusEvt(w, req)
			return
		}

		if req.URL.Path == "/index.doc" {
			if req.URL.RawQuery == "" {
				w.Write([]byte(u.classList()))
			} else {
				w.Write([]byte(u.docEvt(req.URL.RawQuery)))
			}

			return
		}

		if req.URL.Path == "/index.api" {
			w.Write([]byte(u.apiEvt(req)))
			return
		}

		if req.URL.Path == "/index.test" {
			data, err := GetBytes(u.SysPath + "/test.html")
			if err != nil {
				fmt.Println("E>>", u.SysPath+"/test.html")
			}
			w.Write(data)
			return
		}

		//判断是否有可用映射
		if u.hasUrl(req.URL, w, req) {
			return
		}
	}

	path := req.URL.Path

	//value, err := GetBytes(path)
	req.Header.Del("If-Modified-Since")
	//w.Header().Add("Content-Length", strconv.Itoa(len(value)))
	if Substring(path, LastIndex(path, "."), -1) == ".html" {
		w.Header().Add("Content-Type", "text/html; charset=utf-8")
	} else if Substring(path, LastIndex(path, "."), -1) == ".xml" {
		w.Header().Add("Content-Type", "text/xml; charset=utf-8")
	} else if Substring(path, LastIndex(path, "."), -1) == ".css" {
		w.Header().Add("Content-Type", "text/css; charset=utf-8")
	}
	//w.Header().Add("ETag", "1")
	u.fServer.ServeHTTP(w, req)

}

/**
 * 本工程设计使用的类
 */
func (u *JusServer) classList() string {
	u.useClassList = u.useClassList[0:0]
	str := bytes.NewBufferString("")
	path, _ := filepath.Abs(u.SysPath + "/code/")
	u.walkClassFiles(path, path)
	format :=
		`<html>
			<style type="text/css">
				.title{
					padding: 7px;
				    background-color:#eeeeee;
					color:#333333;
				    padding-left: 10px;
				    font-weight: bold;
				}
				tr.debug td{
					color:#ffffff;
					background-color:#e98c8c;
				}
				
				tr.debug td a{
					color:#ffffff;
				}
				
				tr.complete td{
					background-color:#eeeeee;
				}
				ul{
					overflow:hidden;
					padding:0px;
					margin-top:10px;
					margin-bottom:5px;
					border-bottom:1px solid #dddddd;
				}
				li{
					cursor:pointer;
					margin-left:2px;
					margin-right:2px;
					padding:5px;
					padding-left:10px;
					padding-right:10px;
					list-style:none;
					float:left;
					border:1px solid #dddddd;
					border-bottom:none;
				}
				
				a{
					color:#000000;
					text-decoration: none;
				}
				
				.selected{
					background-color:#eeeeee;
				}
				
				#content{
					border-top:none;
					overflow:auto;
				}
				
				#content .type{
					font-size: 16px;
					margin: 5px;
					margin-top:10px;
					font-weight: bold;
				}
				
				table.gridtable {
					width:100%;
					font-family: verdana,arial,sans-serif;
					font-size:13px;
					color:#333333;
					border-width: 1px;
					border-color: #a9c6c9;
					border-collapse: collapse;
				}
				table.gridtable th {
					letter-spacing:2px;
					border-width: 1px;
					padding: 8px;
					border-style: solid;
					border-color: #a9c6c9;
					background-color: #b7dce1;
					font-weight:bold;
					text-decoration: none;
				}
				table.gridtable td {
					border-width: 1px;
					padding: 8px;
					border-style: solid;
					border-color: #a9c6c9;
				}
				
				table.gridtable td a b{
					color:#ee5500;
				}
				
				.value{
					padding:0px;
					padding-left:5px;
					padding-right:5px;
					display:block;
					float:left;
					border:1px solid #dddddd;
					border-radius:5px;
					margin:2px;	
					background-color:#ffffee;
				}
				
			</style>
			<body>
				<div class="title">
					<a href="/" target="_blank">项目文档</a>
				</div>
				<ul>
					<li id="btn0" onclick="showEvt(0)" class="selected">项目对象</li>
					<li id="btn1" onclick="showEvt(1)">系统对象</li>
					<li id="btn2" onclick="showEvt(2)">项目设置</li>
				</ul>
				<div id="content"  class="tabContent">
					<div id="tab0">
						{@code0}
					</div>
					<div id="tab1" >
						{@code1}
					</div>
					<div id="tab2" >
						<table class="gridtable">
							<tr>
								<th width="100">资源</th><th>路径</th>
							</tr>
							{@info}
						</table><br/>
						<table class="gridtable">
							<tr>
								<th width="100">属性名</th><th>键值</th>
							</tr>
							{@code2}
						</table>
					</div>
				</div>
				<script>
					function resEvt(e){
						document.getElementById("content").style.height = document.body.clientHeight - 100
					}
					
					function showEvt(value){
						btn0.className = "";
						btn1.className = "";
						btn2.className = "";
						tab0.style.display = "none";
						tab1.style.display = "none";
						tab2.style.display = "none";
						document.getElementById("btn" + value).className = "selected"
						document.getElementById("tab" + value).style.display = "block";
					}
					window.addEventListener("resize",resEvt);
					resEvt();
					showEvt(0);
				</script>
			</body>
		</html>`

	//系统信息
	file, _ := filepath.Abs(u.SysPath)
	str.WriteString(
		`<tr>
		<td nowrap>工程路径</td>
		<td nowrap>` + u.RootPath + `</td>
	</tr>
	<tr>
		<td nowrap>库路径</td>
		<td nowrap>` + file + `</td>
	</tr>`)
	format = strings.Replace(format, "{@info}", str.String(), -1)
	str.Reset()
	list := make(map[string][]string)
	attrLst := make([]string, 0)
	for _, v := range u.useClassList {
		if list[v.attributes["type"]] == nil {
			list[v.attributes["type"]] = make([]string, 10)
			if v.attributes["type"] != "" {
				attrLst = append(attrLst, v.attributes["type"])
			}
		}
		arr := list[v.attributes["type"]]
		arr = append(arr, `<tr>
				<td nowrap><a href ='index.doc?$`+v.name+`'>`+v.name+IfStr(v.cls == 1, " <b>[JS]</b>", "")+`</a></td>
				<td nowrap>`+v.info.ModTime().Format("2006-01-02 15:04:05")+`</td>
				<td>`+strings.Replace(strings.TrimSpace(v.comment), "\n", "<br/>", -1)+`</td>
			</tr>`)
		list[v.attributes["type"]] = arr
	}
	attrLst = append(attrLst, "")
	for i, n := range attrLst {
		v := list[n]
		if n == "" {
			n = "Undefined Title"
		}
		str.WriteString("<div class='type'>" + strconv.Itoa(i+1) + ". " + n + "</div>")
		str.WriteString(`<table class="gridtable">
							<tr>
								<th width="350">类名</th><th width="145">时间</th><th>说明</th>
							</tr>`)
		for _, s := range v {
			str.WriteString(s)
		}
		str.WriteString(`</table>`)
	}
	format = strings.Replace(format, "{@code1}", str.String(), -1)
	path, _ = filepath.Abs(u.RootPath + "/code/")
	u.useClassList = u.useClassList[0:0]
	u.walkClassFiles(path, path)
	list = make(map[string][]string)
	attrLst = make([]string, 0)
	for _, v := range u.useClassList {
		if list[v.attributes["type"]] == nil {
			list[v.attributes["type"]] = make([]string, 10)
			if v.attributes["type"] != "" {
				attrLst = append(attrLst, v.attributes["type"])
			}
		}
		arr := list[v.attributes["type"]]
		cls := ""
		if v.attributes["status"] == "debug" {
			cls = "debug"
		} else if v.attributes["status"] == "complete" {
			cls = "complete"
		} else {

		}
		arr = append(arr, `<tr class="`+cls+`">
				<td nowrap><a href ='index.doc?`+v.name+`'>`+v.name+IfStr(v.cls == 1, " <b>[JS]</b>", "")+`</a></td>
				<td nowrap>`+v.info.ModTime().Format("2006-01-02 15:04:05")+`</td>
				<td>`+strings.Replace(strings.TrimSpace(v.comment), "\n", "<br/>", -1)+`</td>
			</tr>`)
		list[v.attributes["type"]] = arr
	}
	str.Reset()
	attrLst = append(attrLst, "")
	for i, n := range attrLst {
		v := list[n]
		if n == "" {
			n = "Undefined Title"
		}
		str.WriteString("<div class='type'>" + strconv.Itoa(i+1) + ". " + n + "</div>")
		str.WriteString(`<table class="gridtable">
							<tr>
								<th width="350">类名</th><th width="145">时间</th><th>说明</th>
							</tr>`)
		for _, s := range v {
			str.WriteString(s)
		}
		str.WriteString(`</table>`)
	}
	format = strings.Replace(format, "{@code0}", str.String(), -1)

	//项目设置
	str.Reset()
	ts := ""
	for _, v := range u.GetData() {
		str.WriteString(`<tr>
				<td nowrap>` + v[0] + `</td>`)

		for _, n := range v[1:] {
			ts += `<span class='value'>` + n + `</span>`
		}
		str.WriteString(`<td>` + ts + `</td></tr>`)
		ts = ""
	}
	format = strings.Replace(format, "{@code2}", str.String(), -1)
	return format
}

func (u *JusServer) walkClassFiles(src string, pt string) {
	commet := ""
	filepath.Walk(pt,
		func(f string, fi os.FileInfo, err error) error { //遍历目录
			dPath := Substring(f, StringLen(pt), -1)

			if dPath == "" {
				return nil
			}

			if fi.IsDir() {
				//u.walkClassFiles(src, f)
			} else {
				if path.Ext(f) == ".html" {
					len := StringLen(src)
					commet = readCommentForHTML(f)
					u.useClassList = append(u.useClassList, &element{strings.Replace(Substring(f, len+1, StringLen(f)-5), "\\", ".", -1), fi, commet, toAttrbutes(commet), 0})
				} else if path.Ext(f) == ".js" && !Exist(Substring(f, 0, StringLen(f)-3)+".html") {
					len := StringLen(src)
					commet = readCommentForJS(f)
					u.useClassList = append(u.useClassList, &element{strings.Replace(Substring(f, len+1, StringLen(f)-3), "\\", ".", -1), fi, commet, toAttrbutes(commet), 1})
				}
			}

			return nil

		})
}

/**
 * 读取HTML文件头注释
 */
func readCommentForHTML(f string) string {
	d, err := GetCode(f)
	if err != nil {
		return "-"
	}

	html := &HTML{}
	html.ReadFromString(d)
	list := html.Filter("!")

	sb := ""
	for _, v := range list {
		sb += v.Text()
	}
	return sb

}

/**
 * 读取JS文件头注释
 */
func readCommentForJS(f string) string {
	d, err := GetCode(f)
	if err != nil {
		return "-"
	}
	end := []rune{'*', '/'}
	pos := 0
	sb := bytes.NewBufferString("")
	data := []rune(d)
	position := 0
	var ch rune
f1:
	for position < len(data) {
		ch = data[position]
		position++
		if ch == ' ' || ch == '\t' {
			continue
		}
		if ch != '/' {
			break
		} else {
			for position < len(data) {
				ch = data[position]
				position++
				if ch == '\r' || ch == '\n' {
					break
				}
			}
			for position < len(data) {
				ch = data[position]
				position++
				if ch == end[pos] {
					pos++
					if pos == 2 {
						break f1
					}
					continue
				} else {
					pos = 0
				}
				sb.WriteRune(ch)
			}
		}
	}
	return sb.String()

}

/**
 * 将字符串转换为map
 */
func toAttrbutes(f string) map[string]string {
	var attr = make(map[string]string)
	var char = []rune(f)
	var pos = 0
	var v rune
	buf := bytes.NewBufferString("")
	name := ""
	value := ""
	for pos < len(char) {
		v = char[pos]
		pos++
		if v == '@' {
			//读取关键字
			for pos < len(char) {
				v = char[pos]
				pos++
				if v == ' ' || v == '\t' {
					break
				}
				buf.WriteRune(v)
			}
			name = buf.String()
			buf.Reset()
			//读取后续内容
			for pos < len(char) {
				v = char[pos]
				pos++
				if v == '\r' || v == '\n' {
					break
				}
				buf.WriteRune(v)
			}
			value = strings.TrimSpace(buf.String())
			buf.Reset()
			attr[name] = value
		}
	}
	return attr
}

/**
 * 返回服务服务器使用协议http 或者https
 */
func (u *JusServer) GetProtocol() string {
	if u.protocol == "" {
		return "http"
	}
	return u.protocol
}

/**
 * 显示类的使用说明
 */
func (u *JusServer) docEvt(className string) string {
	//fmt.Println(">>", className)
	api := &APIlist{}
	api.CreateFrom(u, className)
	return api.ToString()
}

/**
 * 获取编辑界面的内容
 */
func (u *JusServer) editDirEvt(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path == "/index.edit/" {
		http.Redirect(w, req, "/index.edit/index.html", http.StatusFound)
		return
	}
	path := u.SysPath + Substring(req.RequestURI, len("/index.edit"), -1)

	value, err := GetBytes(path)

	if err != nil {
		w.WriteHeader(404)
		value = []byte("<h1>404</h1>")
	} else {
		if Substring(path, LastIndex(path, "."), -1) == ".html" {
			w.Header().Add("Content-Type", "text/html; charset=utf-8")
		} else if Substring(path, LastIndex(path, "."), -1) == ".xml" {
			w.Header().Add("Content-Type", "text/xml; charset=utf-8")
		} else if Substring(path, LastIndex(path, "."), -1) == ".css" {
			w.Header().Add("Content-Type", "text/css; charset=utf-8")
		}

	}
	w.Write(value)

}

/**
 * server 控制api调用接口
 */
func (u *JusServer) apiEvt(req *http.Request) string {
	switch req.FormValue("do") {
	case "ls":
		return u.getDirList(u.RootPath + req.FormValue("path"))
	case "getCode": //获取文件内容
		fmt.Println(u.RootPath + req.FormValue("path"))
		value, err := GetCode(u.RootPath + req.FormValue("path"))
		if err == nil {
			return value
		} else {
			return ""
		}
	case "module":
		jus := &JUS{SYSTEM_PATH: u.SysPath, CLASS_PATH: u.SysPath + "/code/"}
		className := Substring(req.RequestURI, StringLen(u.jusDirName), LastIndex(req.RequestURI, "."))
		className = Replace(className, "/", ".")
		if jus.CreateFromString(u.RootPath+"/code/", "", nil, req.FormValue("value"), className) {
			jus.resPath = "code"
			return jus.ToFormatString()
		} else {
			fmt.Println("不存在", className)
		}
		jus = nil
		return ""
	default:
		fmt.Println(">>", req.URL.RawQuery)
	}
	return ""
}

/**
 * 获取文件夹路径列表XML
 */
func (u *JusServer) getDirList(path string) string {
	sb := ""
	lst, err := ioutil.ReadDir(path)
	if err == nil {
		for _, f := range lst {
			if f.IsDir() {
				sb = "<data>" +
					"<name>" + f.Name() + "</name>" +
					"<path>" + Substring(path+f.Name(), StringLen(u.RootPath), -1) + "/</path>" +
					"<isdir>" + strconv.FormatBool(f.IsDir()) + "</isdir>" +
					"</data>" + sb
			} else {
				sb += "<data>"
				sb += "<name>" + f.Name() + "</name>"
				sb += "<path>" + Substring(path+f.Name(), StringLen(u.RootPath), -1) + "</path>"
				sb += "<isdir>" + strconv.FormatBool(f.IsDir()) + "</isdir>"
				sb += "</data>"
			}

		}
	}
	return "<?xml version='1.0' encoding='utf-8' ?><response>" + sb + "</response>"
}

/**
 * 判断是否有反向代理
 */
func (u *JusServer) hasProxy(w http.ResponseWriter, req *http.Request) bool {
	var p *proxyMap = nil
	for _, v := range u.proxy {
		if Index(req.Host, v.pattern.Host) == 0 {
			p = v
			break
		}
	}
	if p != nil {
		if p.cls == 0 {
			path := p.path + req.URL.Path + req.URL.RawQuery
			fmt.Println(">>", path)
			value, err := GetBytes(path)
			if err != nil {
				value = []byte("<h1>404</h1>")
			}
			w.WriteHeader(404)
			w.Write(value)
		} else {
			remote, err := url.Parse(p.path)
			if err != nil {
				panic(err)
			}
			proxy := httputil.NewSingleHostReverseProxy(remote)
			proxy.ServeHTTP(w, req)

		}
		return true
	}
	return false
}

/**
 * 判断是否有可用映射
 */
func (u *JusServer) hasUrl(urlPath *url.URL, w http.ResponseWriter, req *http.Request) bool {
	var p *urlMap = nil
	for _, v := range u.pattern {
		if Index(urlPath.Path, v.pattern) == 0 {
			p = v
			break
		}
	}

	if p != nil {
		if p.cls == 0 {
			path := p.path + Substring(urlPath.Path, StringLen(p.pattern), -1) + urlPath.RawQuery

			value, err := GetBytes(path)
			if err != nil {
				value = []byte("<h1>404</h1>")
			}
			w.WriteHeader(404)
			w.Write(value)
		} else {
			remote, err := url.Parse(p.path)
			if err != nil {
				panic(err)
			}
			proxy := httputil.NewSingleHostReverseProxy(remote)
			req.URL.Path = Substring(urlPath.Path, StringLen(p.pattern), -1)
			proxy.ServeHTTP(w, req)

		}
		return true
	}
	return false
}

/**
 * 获取项目属性信息
 * @param	项目属性
 */
func (u *JusServer) GetAttr(attr string) []string {
	list := u.GetData()
	for _, v := range list {
		if v[0] == attr {
			return v[1:]
		}
	}
	return make([]string, 0)
}

/**
 * 获取项目相似的Attr
 * @param	项目属性
 */
func (u *JusServer) GetAttrLike(attr string) [][]string {
	list := u.GetData()
	lst := make([][]string, 0)
	for _, v := range list {
		if len(v) > 0 && Index(v[0], attr) == 0 {
			lst = append(lst, v[1:])
		}
	}
	return lst
}

/**
 * 发布此工程
 */
func (u *JusServer) Release() {
	for _, v := range u.GetAttr("release-path") {

		if v != "" {
			os.MkdirAll(v, 0777)
		}
		Copy(u.RootPath, v, u.RootPath+"/code/")

		jusPath := v + u.jusDirName + "/"
		if u.RootPath != "" {
			os.MkdirAll(jusPath, 0777)
		}

		//发布Code,先遍历
		u.WalkFiles(u.RootPath+"/code/", jusPath)
	}

}

func (u *JusServer) WalkFiles(src string, dest string) {
	fileType := ""
	filepath.Walk(src,
		func(f string, fi os.FileInfo, err error) error { //遍历目录
			dPath := Substring(f, StringLen(src), -1)
			//fmt.Println(">>", f, ">>", dPath, fi.Name())

			if dPath == "" {
				return nil
			}
			aPath := dest + "/" + dPath

			if fi.IsDir() {
				os.MkdirAll(aPath, 0777) //建立文件目录
				//WalkFiles(f, aPath, "")
			} else {
				//fmt.Println(dPath)
				fileType = Substring(aPath, LastIndex(aPath, "."), -1)
				if fileType == ".html" || fileType == ".js" || fileType == ".css" { //2018-5-4
					d, _ := os.Create(aPath)
					d.Write(relEvt(u.SysPath, u.RootPath, u.jusDirName, dPath))
					defer d.Close()
				} else {
					CopyFile(aPath, f)
				}
			}

			return nil

		})
}

func relEvt(sysPath string, rootPath string, jusDirName string, path string) []byte {
	jus := &JUS{SYSTEM_PATH: sysPath, CLASS_PATH: sysPath + "/code/"}
	lp := LastIndex(path, ".")
	className := Substring(path, 0, lp)
	fmt.Println("export:", className)
	if jus.CreateFrom(rootPath+"/code/", "", nil, className) {
		jus.resPath = "juis"
		return jus.ToFormatBytes()
	}

	return []byte("nothing.")
}

/**
 * 命令代码
 */
func (u *JusServer) CommandEvt(value string) bool {
	cmds := FmtCmd(value)
	if len(cmds) > 0 {
		switch cmds[0] {
		case "stop":
			return false
		case "ls":
			return true
		case "cd":
			return true
		case "crp": //创建一个工程
			return true
		case "stp": //设置工程目录
			fmt.Println(cmds)
			if len(cmds) > 1 {
				u.RootPath = cmds[1]
			}
			fmt.Println("Reset Default Project Path :", u.RootPath)
			return true
		case "run": //运行工程
			u.Start(cmds[1])

			return true
		case "color":
			fmt.Println()
			return true
		case "gc":
			runtime.GC()
			fmt.Println("GC OK.")
			return true
		default:

			return true
		}
	}

	return true

}

/**
 * 获取项目信息
 */
func (u *JusServer) GetData() [][]string {
	data, err := GetCode(u.RootPath + "/.jus")
	if err != nil {
		fmt.Println(err)
		return nil
	}
	return FmtCmdList(data)
}

/**
 * 增加域名级别虚拟目录和反向代理
 */
func (u *JusServer) AddDomainProxy(pattern string, path string) {
	fmt.Println("proxy", pattern, "-->", path)
	cls := 0
	if Index(path, "http://") == 0 {
		cls = 1
	}
	proxyURL, err := url.Parse(pattern)
	if err == nil {
		u.proxy = append(u.proxy, &proxyMap{proxyURL, path, cls})
	} else {
		fmt.Println("Format URL:", pattern)
	}

}

/**
 * 增加虚拟目录和反向代理
 */
func (u *JusServer) AddProxy(pattern string, path string) {
	fmt.Println("pattern", pattern, "-->", path)
	cls := 0
	if Index(path, "http://") == 0 {
		cls = 1
	}
	u.pattern[pattern] = &urlMap{pattern, path, cls}
}

/**
 * 设置环境变量
 */
func (u *JusServer) SetData(cmds []string) {
	data, err := GetCode(u.RootPath + "/.jus")
	if err != nil {
		fmt.Println(err)
		return
	}
	var pos int = 0
	var obj []string = nil
	command := FmtCmdList(data)

	for i, v := range command {
		if len(v) > 0 && cmds[0] == v[0] {
			pos = i
			obj = v
			break
		}
	}

	if obj == nil {
		command = append(command, cmds)
	} else {
		command[pos] = cmds
	}

	if Index(cmds[0], "pattern") == 0 {
		u.AddProxy(cmds[1], cmds[2])
	}

	//对源文件备份
	os.Rename(u.RootPath+"/.jus", u.RootPath+"/.jusb")
	//生成新文件
	f, e := os.Create(u.RootPath + "/.jus")
	defer f.Close()
	if e == nil {
		sb := ""
		for i, l := range command {
			for _, n := range l {
				if Index(n, " ") != -1 {
					n = "\"" + n + "\""
				}
				sb += n + " "

			}
			if i+1 != len(command) {
				sb += "\r\n"
			}

		}
		f.WriteString(sb)
		os.Remove(u.RootPath + "/.jusb")
	}
}

/**
 * 移除环境变量
 */
func (u *JusServer) RetData(cmds []string) bool {
	success := false
	data, err := GetCode(u.RootPath + "/.jus")
	if err != nil {
		fmt.Println(err)
		return success
	}

	lst := make([][]string, 0)
	command := FmtCmdList(data)

	for _, v := range command {
		if len(v) == 0 {
			continue
		}

		if cmds[0] == v[0] {
			success = true
			continue
		}

		lst = append(lst, v)
	}

	if len(command) != len(lst) {
		command = lst
		//对源文件备份
		os.Rename(u.RootPath+"/.jus", u.RootPath+"/.jusb")
		//生成新文件
		f, e := os.Create(u.RootPath + "/.jus")
		defer f.Close()
		if e == nil {
			sb := ""
			for i, l := range command {
				for _, n := range l {
					if Index(n, " ") != -1 {
						n = "\"" + n + "\""
					}
					sb += n + " "

				}
				if i+1 != len(command) {
					sb += "\r\n"
				}

			}
			f.WriteString(sb)
			os.Remove(u.RootPath + "/.jusb")
		}
	}

	return success

}
