package web

import (
	"crypto/sha256"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"github.com/BKXSupplyChain/Energy/backend/conf"
	"github.com/BKXSupplyChain/Energy/db"
	"github.com/BKXSupplyChain/Energy/types"
	"github.com/BKXSupplyChain/Energy/utils"
	"io"
	"io/ioutil"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"math/big"
	"strconv"

	eth "github.com/ethereum/go-ethereum/crypto"
)

func serveFile(path string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, path)
	}
}

func getUser(r *http.Request) (types.UserData, error) {
	name, errU := r.Cookie("username")
	password, errP := r.Cookie("password")
	if errU != nil || errP != nil {
		return types.UserData{}, errors.New("Auth required")
	}

	id := name.Value
	var user types.UserData
	if db.Get(&user, string(id)) != nil {
		return types.UserData{}, errors.New("No such user")
	}
	if user.Username != name.Value || user.PasswordHash != sha256.Sum256([]byte(password.Value)) {
		return types.UserData{}, errors.New("Wrong password")
	}
	return user, nil
}

func loginUser(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	http.SetCookie(w, &http.Cookie{
		Name:  "username",
		Value: r.Form.Get("username"),
		Path:  "/",
	})
	http.SetCookie(w, &http.Cookie{
		Name:  "password",
		Value: r.Form.Get("password"),
		Path:  "/",
	})
	http.Redirect(w, r, "/main", 307)
}

func formatFloat(a float64) string {
	pow := int(math.Floor(math.Log10(a) / 3))
	if -3 <= pow && pow <= 4 {
		return fmt.Sprintf("%f", a*math.Pow10(-pow*3))[:4] + []string{" n", " μ", " m", " ", " k", " M", " G", " T"}[pow+3]
	} else {
		return fmt.Sprintf("%g ", a)
	}
}

func mainData(w http.ResponseWriter, r *http.Request) {
	user, err := getUser(r)
	if err != nil {
		http.Redirect(w, r, "/?err=Auth error", 307)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte("["))
	for _, socketID := range user.Sockets {
		var soc types.SocketInfo
		db.Get(&soc, socketID)
		w.Write([]byte(fmt.Sprintf("[\"%s\"", soc.Alias)))
		w.Write([]byte(fmt.Sprintf(", \"%sJ/s\"", formatFloat(float64(db.TokenGetPower(user.Username, time.Now().Unix()-10))))))
		if soc.ActiveProposal != "" {
			var prop types.Proposal
			db.Get(&prop, soc.ActiveProposal)
			w.Write([]byte(fmt.Sprintf(", \"%sGwei/J\"", formatFloat(float64(prop.Price)/1e9))))
		} else {
			w.Write([]byte(", \"NC\""))
		}
		w.Write([]byte(fmt.Sprintf("[\"%s\"", socketID)))
	}
	w.Write([]byte("]"))
}

func mainPage(w http.ResponseWriter, r *http.Request) {
	_, err := getUser(r)
	if err != nil {
		http.Redirect(w, r, "/?err="+err.Error(), 307)
		return
	}
	pattern, _ := ioutil.ReadFile("./web/static/main.html")
	w.Write([]byte(fmt.Sprintf(string(pattern), conf.GetSelfAddress())))
}

func registerUser(w http.ResponseWriter, r *http.Request) {
	var user types.UserData
	r.ParseForm()
	user.Username = r.Form.Get("username")
	id := user.Username
	user.PasswordHash = sha256.Sum256([]byte(r.Form.Get("password")))
	user.PrivateKey = r.Form.Get("privkey")
	if db.Add(&user, string(id)) != nil {
		http.Redirect(w, r, "/register?err=Username is reserved", 307)
		return
	}
	loginUser(w, r)
}

func registerPage(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.RawQuery, "err=") {
		text, err := url.QueryUnescape(r.URL.RawQuery[4:])
		if err == nil {
			w.Write([]byte("<div class=\"err\">" + text + "</div>"))
		}
	}
	file, _ := os.Open("./web/static/register.html")
	io.Copy(w, file)
}

func loginPage(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.RawQuery, "err=") {
		text, err := url.QueryUnescape(r.URL.RawQuery[4:])
		if err == nil {
			w.Write([]byte("<div class=\"err\">" + text + "</div>"))
		}
	}
	file, _ := os.Open("./web/static/login.html")
	io.Copy(w, file)
}

func changePassword(w http.ResponseWriter, r *http.Request) {
	user, err := getUser(r)
	if err != nil {
		http.Redirect(w, r, "/?err="+err.Error(), 307)
		return
	}
	if r.ParseForm() != nil {
		http.Redirect(w, r, "/personal", 307)
	}
	user.PasswordHash = sha256.Sum256([]byte(r.Form.Get("new_password")))
	http.SetCookie(w, &http.Cookie{
		Name:  "password",
		Value: r.Form.Get("new_password"),
		Path:  "/",
	})
	w.Write([]byte("<h1>Password changed!</h1></br><a href = \"/main\">To main</a>"))
}

func createProposal(w http.ResponseWriter, r *http.Request) {
	var proposal types.Proposal
	r.ParseForm()
	i, err := strconv.ParseUint(r.Form.Get("price"), 10, 64)
	utils.CheckFatal(err)
	proposal.Price = i
	relErr, err := strconv.ParseFloat(r.Form.Get("relerror"), 32)
	utils.CheckFatal(err)
	proposal.RelError = relErr
	bigI := new(big.Int)
	bigI, errs := bigI.SetString(r.Form.Get("abserror"), 10)
	proposal.AbsError = *bigI
	if errs {
		log.Fatal("Conversion error")
	}
	i, err = strconv.ParseUint(r.Form.Get("durability"), 10, 64)
	utils.CheckFatal(err)
	proposal.TTL = i
	bigI = new(big.Int)
	bigI, errs = bigI.SetString(r.Form.Get("amount"), 10)
	proposal.TotalAmount = *bigI
	utils.CheckFatal(err)
	user, err := getUser(r)
	utils.CheckFatal(err)
	if db.Add(&proposal, user.Username) != nil { 
		log.Println("Failed to add propose")
	}
	pKey, err := eth.HexToECDSA(user.PrivateKey)
	utils.CheckFatal(err)
	proposal.ID = db.GetNewID()
	sendProposal(proposal, r.Form.Get("socketId"), pKey.PublicKey)
	http.Redirect(w, r, "/main", 307)
}

func proposalPage(w http.ResponseWriter, r *http.Request) {
	serveFile("./web/static/proposal.html")(w, r)
}

func contractPage(w http.ResponseWriter, r *http.Request) {
	serveFile("./web/static/contract.html")(w, r)
}

func concludeContract(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/main", 307);
}

func contractData(w http.ResponseWriter, r *http.Request) {
	//здесь забираем данные о текущем контракте
}

func sendProposal(proposal types.Proposal, socID string, key ecdsa.PublicKey)  {
	log.Println("SEND PROPOSAL", proposal, socID, key)
}

func socketPage(w http.ResponseWriter, r *http.Request) {
	serveFile("./web/static/socket.html")
}

func socketData(w http.ResponseWriter, r *http.Request) {
	socketID = r.Body()
	user, err := getUser(r)
	if err != nil {
		http.Redirect(w, r, "/?err=Auth error", 307)
		return
	}
	var soc types.SocketInfo
	db.Get(&soc, socketID)
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte("["))
	w.Write([]byte(fmt.Sprintf("[\"%s\"", soc.SensorID)))
	w.Write([]byte(fmt.Sprintf("[\"%s\"", soc.Owner)))
	w.Write([]byte(fmt.Sprintf("[\"%s\"", soc.Alias)))
	w.Write([]byte(fmt.Sprintf("[\"%s\"", soc.NeighborAddr)))
	w.Write([]byte(fmt.Sprintf("[\"%s\"", soc.NeighborKey)))
	w.Write([]byte(fmt.Sprintf("[\"%s\"", soc.ActiveContract)))
	w.Write([]byte("]"))
}

func Serve() {
	http.HandleFunc("/", loginPage)
	http.HandleFunc("/login/impl", loginUser)
	http.HandleFunc("/shooter", serveFile("./web/static/shooter.html"))
	http.HandleFunc("/register", registerPage)
	http.HandleFunc("/register/impl", registerUser)
	http.HandleFunc("/main", mainPage)
	http.HandleFunc("/mainData", mainData)
	http.HandleFunc("/personal", serveFile("./web/static/personal.html"))
	http.HandleFunc("/personal/chpass", changePassword)
	http.HandleFunc("/style.css", serveFile("./web/static/style.css"))
	http.HandleFunc("/proposal/impl", createProposal)
	http.HandleFunc("/proposal", proposalPage)
	http.HandleFunc("/contract", contractPage)
	http.HandleFunc("/contract/impl", concludeContract)
	http.HandleFunc("/contractData", contractData)
	http.HandleFunc("/socket", socketPage)
	http.ListenAndServe(":80", nil)
}
