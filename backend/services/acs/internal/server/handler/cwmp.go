package handler

import (
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"oktopUSP/backend/services/acs/internal/auth"
	"oktopUSP/backend/services/acs/internal/cwmp"
	"strings"
	"time"

	"github.com/oleiade/lane"
)

func (h *Handler) CwmpHandler(w http.ResponseWriter, r *http.Request) {

	log.Printf("--> Connection from %s", r.RemoteAddr)

	defer r.Body.Close()
	defer log.Printf("<-- Connection from %s closed", r.RemoteAddr)

	tmp, _ := ioutil.ReadAll(r.Body)
	body := string(tmp)

	if h.acsConfig.DebugMode {
		log.Println("Received message: ", body)
	}

	var envelope cwmp.SoapEnvelope
	xml.Unmarshal(tmp, &envelope)

	messageType := envelope.Body.CWMPMessage.XMLName.Local
	log.Println("messageType: ", messageType)

	var cpe CPE
	var exists bool

	w.Header().Set("Server", "Oktopus "+Version)

	if messageType != "Inform" {
		if cookie, err := r.Cookie("oktopus"); err == nil {
			if cpe, exists = h.Cpes[cookie.Value]; !exists {
				log.Printf("CPE with serial number %s not found", cookie.Value)
			}
			log.Printf("CPE with serial number %s found", cookie.Value)
		} else {
			fmt.Println("cookie 'oktopus' missing")
			w.WriteHeader(401)
			return
		}
	}

	if messageType == "Inform" {
		var Inform cwmp.CWMPInform
		xml.Unmarshal(tmp, &Inform)

		addr := peerIP(r)

		sn := Inform.DeviceId.SerialNumber

		if cpe, exists = h.Cpes[sn]; !exists {
			log.Println("New device: " + sn)
			cpe = CPE{
				SerialNumber:    sn,
				SoftwareVersion: Inform.GetSoftwareVersion(),
				HardwareVersion: Inform.GetHardwareVersion(),
				OUI:             Inform.DeviceId.OUI,
				Queue:           lane.NewQueue(),
				DataModel:       Inform.GetDataModelType(),
			}
			h.Cpes[sn] = cpe
			h.pub(NATS_CWMP_SUBJECT_PREFIX+sn+".info", tmp)
		}

		cpe.ExternalIPAddress = addr
		cpe.ConnectionRequestURL = Inform.GetConnectionRequest()
		cpe.Username = Inform.GetConnectionRequestUsername()
		cpe.Password = Inform.GetConnectionRequestPassword()

		log.Printf("Received an Inform from device %s withEventCodes %s", addr, Inform.GetEvents())

		expiration := time.Now().AddDate(0, 0, 1)

		cookie := http.Cookie{Name: "oktopus", Value: sn, Expires: expiration}
		http.SetCookie(w, &cookie)
		//data, _ := xml.Marshal(cwmp.InformResponse(envelope.Header.Id))
		fmt.Fprintf(w, cwmp.InformResponse(envelope.Header.Id))

	} else if messageType == "TransferComplete" {

	} else if messageType == "GetRPC" {

	} else {

		if len(body) == 0 {
			log.Println("Got Empty Post")
		}

		if cpe.Waiting != nil {

			log.Println("ACS was waiting for a response from the CPE, now received something")

			var e cwmp.SoapEnvelope
			xml.Unmarshal([]byte(body), &e)
			log.Println("Kind of envelope: ", e.KindOf())

			if e.KindOf() == "GetParameterNamesResponse" {
				log.Println("Receive GetParameterNamesResponse from CPE:", cpe.SerialNumber)
				msgAnswer(cpe.Waiting.Callback, cpe.Waiting.Time, h.acsConfig.DeviceAnswerTimeout, tmp)
			} else if e.KindOf() == "GetParameterValuesResponse" {
				log.Println("Receive GetParameterValuesResponse from CPE:", cpe.SerialNumber)
				msgAnswer(cpe.Waiting.Callback, cpe.Waiting.Time, h.acsConfig.DeviceAnswerTimeout, tmp)
			} else if e.KindOf() == "SetParameterValuesResponse" {
				log.Println("Receive SetParameterValuesResponse from CPE:", cpe.SerialNumber)
				msgAnswer(cpe.Waiting.Callback, cpe.Waiting.Time, h.acsConfig.DeviceAnswerTimeout, tmp)
			} else if e.KindOf() == "Fault" {
				log.Println("Receive FaultResponse from CPE:", cpe.SerialNumber)
				msgAnswer(cpe.Waiting.Callback, cpe.Waiting.Time, h.acsConfig.DeviceAnswerTimeout, tmp)
				log.Println(body)
			} else {
				log.Println("Unknown message type")
				log.Println("Body:", body)
				msgAnswer(cpe.Waiting.Callback, cpe.Waiting.Time, h.acsConfig.DeviceAnswerTimeout, tmp)
			}
			cpe.Waiting = nil
		} else {
			log.Println("CPE was not waiting for a response")
		}

		log.Printf("CPE %s Queue size: %d", cpe.SerialNumber, cpe.Queue.Size())

		if cpe.Queue.Size() > 0 {
			req := cpe.Queue.Dequeue().(Request)
			cpe.Waiting = &req
			log.Println("Sending request to CPE:", req.Id)
			w.Header().Set("Connection", "keep-alive")
			w.Write(req.CwmpMsg)
		} else {
			w.Header().Set("Connection", "close")
			w.WriteHeader(204)
		}
	}
	cpe.LastConnection = time.Now()
	h.Cpes[cpe.SerialNumber] = cpe
}

func (h *Handler) ConnectionRequest(cpe CPE) error {
	username := cpe.Username
	password := cpe.Password
	// fall back to global config if CPE has no per-device credentials
	if username == "" {
		username = h.acsConfig.ConnReqUsername
	}
	if password == "" {
		password = h.acsConfig.ConnReqPassword
	}

	connReqURL := reachableConnReqURL(cpe.ConnectionRequestURL, cpe.ExternalIPAddress)

	log.Printf("[ConnReq] --> CPE: %s, URL: %s", cpe.SerialNumber, connReqURL)
	log.Printf("[ConnReq] Using credentials - user: %q (source: %s)", username, func() string {
		if cpe.Username != "" {
			return "per-CPE from Inform"
		}
		return "global config"
	}())

	ok, err := auth.Auth(username, password, connReqURL)
	if !ok {
		cpe.Queue.Dequeue()
		log.Println("Error while authenticating to CPE, err:", err)
	} else {
		log.Println("<-- Successfully authenticated to CPE", cpe.SerialNumber)
	}

	return err
}

// peerIP returns the CPE's address as seen by the ACS, without the source port.
func peerIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-Ip"); ip != "" {
		return ip
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// reachableConnReqURL replaces the host of the ConnectionRequestURL reported by
// the CPE when that host is not something the ACS can dial. CPEs that fail to
// resolve their own interface address (e.g. easycwmp with a misconfigured
// 'interface' option) report the wildcard bind address instead, which would make
// the ACS connect to itself. The address the Inform came from is used instead.
func reachableConnReqURL(rawURL, cpeAddr string) string {
	if rawURL == "" || cpeAddr == "" {
		return rawURL
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		log.Printf("[ConnReq] Could not parse ConnectionRequestURL %q: %v", rawURL, err)
		return rawURL
	}

	host := u.Hostname()
	switch host {
	case "", "0.0.0.0", "::", "127.0.0.1", "::1", "localhost":
	default:
		return rawURL
	}

	if port := u.Port(); port != "" {
		u.Host = net.JoinHostPort(cpeAddr, port)
	} else if strings.Contains(cpeAddr, ":") {
		u.Host = "[" + cpeAddr + "]"
	} else {
		u.Host = cpeAddr
	}

	log.Printf("[ConnReq] CPE reported unreachable host %q, dialing %q instead", host, u.Host)

	return u.String()
}

func msgAnswer(
	callback chan []byte,
	timeMsgWasSent time.Time,
	timeOut time.Duration,
	msgAnswer []byte,
) {
	if time.Since(timeMsgWasSent) > timeOut {
		log.Println("CPE took too long to answer the request, the message will be discarded")
	} else {
		callback <- msgAnswer
	}
}
