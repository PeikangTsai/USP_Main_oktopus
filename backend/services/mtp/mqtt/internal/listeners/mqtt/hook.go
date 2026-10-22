package mqtt

import (
	"broker/internal/config"
	"broker/internal/nats"
	"bytes"
	"context"
	"log"
	"strings"

	"github.com/mochi-co/mqtt/v2"
	"github.com/mochi-co/mqtt/v2/hooks/auth"
	"github.com/mochi-co/mqtt/v2/packets"
	"github.com/nats-io/nats.go/jetstream"
)

type MyHook struct {
	mqtt.HookBase
}

type NatsAuthHook struct {
	mqtt.HookBase
	kv jetstream.KeyValue
}

func (h *MyHook) ID() string {
	return "events-controller"
}

func (h *MyHook) Provides(b byte) bool {
	return bytes.Contains([]byte{
		mqtt.OnSubscribed,
		mqtt.OnDisconnect,
		mqtt.OnClientExpired,
		mqtt.OnPacketEncode,
	}, []byte{b})
}

func (h *MyHook) Init(config any) error {
	h.Log.Info().Msg("initialised")
	return nil
}

func (h *MyHook) OnClientExpired(cl *mqtt.Client) {
	log.Printf("Client id %s expired", cl.ID)
}

func (h *MyHook) OnDisconnect(cl *mqtt.Client, err error, expire bool) {
	var clUser string
	if len(cl.Properties.Props.User) > 0 {
		clUser = cl.Properties.Props.User[0].Val
	}

	if clUser != "" {
		err := server.Publish("oktopus/usp/v1/status/"+clUser, []byte("0"), false, 1)
		if err != nil {
			log.Println("server publish error: ", err)
		}
	}
}

func (h *MyHook) OnSubscribed(cl *mqtt.Client, pk packets.Packet, reasonCodes []byte) {
	//Verifies if it's a device who is subscribed
	if strings.Contains(pk.Filters[0].Filter, "oktopus/usp/v1/agent") {
		var clUser string

		if len(cl.Properties.Props.User) > 0 {
			clUser = cl.Properties.Props.User[0].Val
		}

		if clUser != "" {
			cl.Properties.Will = mqtt.Will{
				Qos:       1,
				TopicName: "oktopus/usp/v1/status/" + clUser,
				Payload:   []byte("0"),
				Retain:    false,
			}
			log.Println("new device:", clUser)
			err := server.Publish("oktopus/usp/v1/status/"+clUser, []byte("1"), false, 1)
			if err != nil {
				log.Println("server publish error: ", err)
			}
		}

	}
}

func (h *MyHook) OnPacketEncode(cl *mqtt.Client, pk packets.Packet) packets.Packet {
	var clUser string
	if len(cl.Properties.Props.User) > 0 {
		clUser = cl.Properties.Props.User[0].Val
	}
	if pk.FixedHeader.Type == packets.Connack {
		pk.Properties.User = []packets.UserProperty{{Key: "subscribe-topic", Val: "oktopus/usp/v1/agent/" + clUser}}
	}

	return pk
}

func (h *NatsAuthHook) ID() string {
	return "device-auth"
}

func (h *NatsAuthHook) Provides(b byte) bool {
	return bytes.Contains([]byte{
		mqtt.OnConnectAuthenticate,
		mqtt.OnACLCheck,
	}, []byte{b})
}

func (h *NatsAuthHook) Init(c any) error {

	_, _, kv := nats.StartNatsClient(c.(config.Nats))
	h.kv = kv

	h.Log.Info().Msg("initialised device auth nats hook")
	return nil
}

func (h *NatsAuthHook) OnConnectAuthenticate(cl *mqtt.Client, pk packets.Packet) bool {

	username := string(pk.Connect.Username)
	lookupKey := normalizeUsername(username)

	entry, err := h.kv.Get(context.TODO(), lookupKey)
	if err != nil {
		if err == jetstream.ErrKeyNotFound {
			log.Printf("auth failed: user access not found, username=%q key=%q client=%q remote=%s", username, lookupKey, cl.ID, cl.Net.Remote)
			return false
		}
		log.Printf("auth failed: error getting key value: %v, username=%q key=%q client=%q remote=%s", err, username, lookupKey, cl.ID, cl.Net.Remote)
		return false
	}

	if bytes.Equal(entry.Value(), pk.Connect.Password) {
		return true
	}

	log.Printf("auth failed: password mismatch, username=%q key=%q client=%q remote=%s", username, lookupKey, cl.ID, cl.Net.Remote)
	return false
}

func endpointIDFromClient(cl *mqtt.Client) string {
	for _, up := range cl.Properties.Props.User {
		if up.Key == "usp-endpoint-id" {
			return up.Val
		}
	}
	return ""
}
											
func (h *NatsAuthHook) OnACLCheck(cl *mqtt.Client, topic string, write bool) bool {

	username := string(cl.Properties.Username)
	lookupKey := normalizeUsername(username)

	_, err := h.kv.Get(context.TODO(), lookupKey)
	if err != nil {
		if err == jetstream.ErrKeyNotFound {
			log.Println("user access not found, blocked user:", lookupKey)
			return false
		}
		log.Println("error getting key value: ", err)
		return false
	}

	if username == "oktopusController" {
		return true
	}

	endpointID := endpointIDFromClient(cl)

	if !write {
		allowed := []string{"oktopus/usp/v1/agent/" + username}
		if endpointID != "" {
			allowed = append(allowed, "oktopus/usp/v1/agent/"+endpointID)
		}
		for _, a := range allowed {
			log.Printf("[agent]DEBUG ACL check: allowedTopic=%q topic=%q", a, topic)
			if _, ok := auth.MatchTopic(a, topic); ok {
				return true
			}
		}
		return false
	}

	if write {
		deviceAllowedTopics := []string{
			"oktopus/usp/v1/controller",
			"oktopus/usp/v1/api/" + username,
			"oktopus/usp/v1/api/" + endpointID,
		}
		if endpointID != "" {
			    deviceAllowedTopics = append(deviceAllowedTopics, "oktopus/usp/v1/api/"+endpointID)
		}
		for _, allowedTopic := range deviceAllowedTopics {
			log.Printf("[controller/api]DEBUG ACL check: allowedTopic=%q topic=%q", allowedTopic, topic)
			_, ok := auth.MatchTopic(allowedTopic, topic)
			if ok {
				return true
			}
		}
	}

	return false
}

func normalizeUsername(username string) string {
    if strings.Contains(username, "::") {
        parts := strings.Split(username, "-")
        return parts[len(parts)-1]
    }
    return username
}
