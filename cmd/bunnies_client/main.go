package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	//"log"
	"bufio"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	Connector "github.com/RedHatInsights/cloud-connector/internal/mqtt"
	MQTT "github.com/eclipse/paho.mqtt.golang"
)

func NewTLSConfig(certFile string, keyFile string) (*tls.Config, string) {
	// Import trusted certificates from CAfile.pem.
	// Alternatively, manually add CA certificates to
	// default openssl CA bundle.
	/*
	   certpool := x509.NewCertPool()
	   pemCerts, err := ioutil.ReadFile("samplecerts/CAfile.pem")
	   if err == nil {
	       certpool.AppendCertsFromPEM(pemCerts)
	   }
	*/

	// Import client certificate/key pair
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		panic(err)
	}

	// Just to print out the client certificate..
	cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		panic(err)
	}
	//fmt.Println(cert.Leaf)
	fmt.Println(cert.Leaf.Subject.CommonName)

	tlsConfig := &tls.Config{
		// RootCAs = certs used to verify server cert.
		//RootCAs: certpool,
		// ClientAuth = whether to request cert from server.
		// Since the server is set up for SSL, this happens
		// anyways.
		//ClientAuth: tls.NoClientCert,
		// ClientCAs = certs used to validate client cert.
		//ClientCAs: nil,
		// InsecureSkipVerify = verify that cert contents
		// match server. IP matches what is in cert etc.
		InsecureSkipVerify: true,
		// Certificates = list of certs client sends to server.
		Certificates: []tls.Certificate{cert},
	}

	return tlsConfig, cert.Leaf.Subject.CommonName
}

func main() {

	/*
	   logger := log.New(os.Stderr, "", log.LstdFlags)
	   MQTT.ERROR = logger
	   MQTT.CRITICAL = logger
	   MQTT.WARN = logger
	   MQTT.DEBUG = logger
	*/

	connectionCount := flag.Int("connection_count", 1, "number of connections to create")
	broker := flag.String("broker", "tcp://eclipse-mosquitto:1883", "hostname / port of broker")
	certFile := flag.String("cert", "cert.pem", "path to cert file")
	keyFile := flag.String("key", "key.pem", "path to key file")
	flag.Parse()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	for i := 0; i < *connectionCount; i++ {
		go startProducer(*certFile, *keyFile, *broker, i)
	}

	<-c

}

var m MQTT.MessageHandler = func(client MQTT.Client, msg MQTT.Message) {
	fmt.Printf("default handler rec TOPIC: %s MSG:%s\n", msg.Topic(), msg.Payload())
}

func startProducer(certFile string, keyFile string, broker string, i int) {
	tlsconfig, clientID := NewTLSConfig(certFile, keyFile)

	controlReadTopic := fmt.Sprintf("redhat/insights/%s/control/in", clientID)
	controlWriteTopic := fmt.Sprintf("redhat/insights/%s/control/out", clientID)
	fmt.Println("control consumer topic: ", controlReadTopic)

	connOpts := MQTT.NewClientOptions()
	connOpts.AddBroker(broker)
	/*
	   hostname, err := os.Hostname()
	   if err != nil {
	       panic("Unable to determine hostname:" + err.Error())
	   }
	*/

	//username := fmt.Sprintf("client-%d", i)

	//clientID := fmt.Sprintf("client-%s-%d", hostname, i)

	connOpts.SetClientID(clientID).SetTLSConfig(tlsconfig)
	//connOpts.SetCleanSession(true)
	//connOpts.SetUsername(username)
	//connOpts.SetPassword(username)
	//connOpts.SetTLSConfig(&tls.Config{InsecureSkipVerify: true})
	connOpts.SetTLSConfig(tlsconfig)

	connectionStatusMsgPayload := Connector.ConnectionStatusMessageContent{ConnectionState: "offline"}
	lastWillMsg := Connector.ControlMessage{
		MessageType: "connection-status",
		MessageID:   "5678",
		Version:     1,
		Content:     connectionStatusMsgPayload,
	}
	payload, err := json.Marshal(lastWillMsg)

	if err != nil {
		fmt.Println("marshal of message failed, err:", err)
		panic(err)
	}

	connOpts.SetWill(controlWriteTopic, string(payload), byte(0), true)

	//lastWillPayload, err := buildDisconnectMessage(clientID)
	//connOpts.SetWill(writeTopic, string(lastWillPayload), byte(0), false)

	//    connOpts.SetDefaultPublishHandler(m)

	connOpts.OnConnect = func(c MQTT.Client) {
		fmt.Println("*** OnConnect - subscribing to topic:", controlReadTopic)
		if token := c.Subscribe(controlReadTopic, 0, onMessageReceived); token.Wait() && token.Error() != nil {
			panic(token.Error())
		}
	}

	client := MQTT.NewClient(connOpts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		panic(token.Error())
	}
	fmt.Println("Connected to server ", broker)

	/* Verify that this client cannot publish to a different client's topic
	   topic = fmt.Sprintf("redhat/insights/%s/in", "client-10")
	   payload = fmt.Sprintf(`{"id": "%s"}`, "client-10")
	*/

	/* SPOOF the payload
	   spoofPayload := `{"id": "client-NO"}`
	   payload = spoofPayload
	*/

	cf := Connector.CanonicalFacts{
		InsightsID:            "1234",
		MachineID:             "5678",
		BiosID:                "1234",
		SubscriptionManagerID: "3245",
		IpAddresses:           []string{"192.168.68.101"},
		MacAddresses:          []string{"54.54.45.45.62.26"},
		Fqdn:                  "fred.flintstone.com",
	}
	connectionStatusPayload := Connector.ConnectionStatusMessageContent{CanonicalFacts: cf, ConnectionState: "online"}
	connMsg := Connector.ControlMessage{
		MessageType: "connection-status",
		MessageID:   "1234",
		Version:     1,
		Content:     connectionStatusPayload,
	}

	payload, err = json.Marshal(connMsg)

	if err != nil {
		fmt.Println("marshal of message failed, err:", err)
		panic(err)
	}

	fmt.Println("publishing to topic:", controlWriteTopic)
	client.Publish(controlWriteTopic, byte(0), true, payload)
	fmt.Printf("Published message %s... Sleeping...\n", payload)
	time.Sleep(time.Second * 10)
}

func onMessageReceived(client MQTT.Client, message MQTT.Message) {
	fmt.Printf("Received message on topic: %s\nMessage: %s\n", message.Topic(), message.Payload())

	var connMsg Connector.ControlMessage

	if message.Payload() == nil || len(message.Payload()) == 0 {
		fmt.Println("empty payload")
		return
	}

	if err := json.Unmarshal(message.Payload(), &connMsg); err != nil {
		fmt.Println("unmarshal of message failed, err:", err)
		panic(err)
	}

	fmt.Println("Got a message:", connMsg)

	switch connMsg.MessageType {
	case "work":
		fmt.Println("payload: ", connMsg.Content)
		fmt.Printf("type(payload): %T", connMsg.Content)

		payloadBytes := []byte(connMsg.Content.(string))
		var workPayload map[string]interface{}
		if err := json.Unmarshal(payloadBytes, &workPayload); err != nil {
			fmt.Println("FIXME: Unable to parse work payload")
			return
		}

		handler := workPayload["handler"].(string)
		payload_url := workPayload["payload_url"].(string)
		return_url := workPayload["return_url"].(string)

		fmt.Println("handler:", handler)
		fmt.Println("payload_url:", payload_url)
		fmt.Println("return_url:", return_url)

		// FIXME:  WHAT ABOUT MESSAGE_ID???
		resp, err := http.Get(payload_url)
		if err != nil {
			fmt.Println("ERROR downloading playbook: ", err)
			return
		}

		scanner := bufio.NewScanner(resp.Body)
		fmt.Println("---------- BEGIN PLAYBOOK -----------")
		for i := 0; scanner.Scan() && i < 5; i++ {
			fmt.Println(scanner.Text())
		}
		fmt.Println("---------- END PLAYBOOK -----------")
		if err := scanner.Err(); err != nil {
			panic(err)
		}

		fmt.Println("Running playbook...")
		time.Sleep(1 * time.Second)
		fmt.Println("playbook finsihed...")

		outputBody, err := json.Marshal(map[string]string{
			"output": "Run was a success!",
		})

		fmt.Println("Uploading output...")

		client := &http.Client{}
		req, err := http.NewRequest("POST", return_url, bytes.NewBuffer(outputBody))
		req.Header.Add("message_id", connMsg.MessageID)
		req.Header.Add("Content-Type", "application/json")
		resp, err = client.Do(req)

		if err != nil {
			fmt.Println("ERROR sending output back to cloud.redhat.com: ", err)
			return
		}
		fmt.Println("output uploaded...")

		defer resp.Body.Close()

	default:
		fmt.Println("Invalid message type!")
	}
}

func buildDisconnectMessage(clientID string) ([]byte, error) {
	connMsg := Connector.ControlMessage{
		MessageType: "disconnect",
		MessageID:   "4321",
		Version:     1,
	}

	return json.Marshal(connMsg)
}
