package mqtt

import (
	"encoding/json"
	"time"

	"github.com/RedHatInsights/cloud-connector/internal/domain"
	MQTT "github.com/eclipse/paho.mqtt.golang"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

func buildReconnectMessage(delay int) (*uuid.UUID, *ControlMessage, error) {

	args := map[string]int{"delay": delay}

	content := CommandMessageContent{Command: "reconnect", Arguments: args}

	return buildControlMessage(&content)
}

func buildControlMessage(content *CommandMessageContent) (*uuid.UUID, *ControlMessage, error) {

	messageID, err := uuid.NewRandom()
	if err != nil {
		return nil, nil, err
	}

	message := ControlMessage{
		MessageType: "control",
		MessageID:   messageID.String(),
		Version:     1,
		Sent:        time.Now(),
		Content:     content,
	}

	return &messageID, &message, err
}

func buildDataMessage(directive string, metadata interface{}, payload interface{}) (*uuid.UUID, *DataMessage, error) {

	messageID, err := uuid.NewRandom()
	if err != nil {
		return nil, nil, err
	}

	message := DataMessage{
		MessageType: "data",
		MessageID:   messageID.String(),
		Version:     1,
		Sent:        time.Now(),
		Metadata:    metadata,
		Directive:   directive,
		Content:     payload,
	}

	return &messageID, &message, err
}

func sendReconnectMessageToClient(mqttClient MQTT.Client, logger *logrus.Entry, topicBuilder *TopicBuilder, qos byte, clientID domain.ClientID, delay int) error {

	messageID, message, err := buildReconnectMessage(delay)

	if err != nil {
		return err
	}

	logger = logger.WithFields(logrus.Fields{"message_id": messageID, "client_id": clientID})

	logger.Debug("Sending reconnect message to connected client")

	topic := topicBuilder.BuildOutgoingControlTopic(clientID)

	err = sendMessage(mqttClient, logger, clientID, messageID, topic, qos, message)

	return err
}

func sendControlMessage(mqttClient MQTT.Client, logger *logrus.Entry, topic string, qos byte, clientID domain.ClientID, content *CommandMessageContent) (*uuid.UUID, error) {

	messageID, message, err := buildControlMessage(content)

	if err != nil {
		return nil, err
	}

	logger = logger.WithFields(logrus.Fields{"message_id": messageID, "client_id": clientID})

	logger.Debug("Sending control message to connected client")

	err = sendMessage(mqttClient, logger, clientID, messageID, topic, qos, message)

	return messageID, err
}

func sendMessage(mqttClient MQTT.Client, logger *logrus.Entry, clientID domain.ClientID, messageID *uuid.UUID, topic string, qos byte, message interface{}) error {

	logger = logger.WithFields(logrus.Fields{"message_id": messageID, "client_id": clientID})

	messageBytes, err := json.Marshal(message)
	if err != nil {
		return err
	}

	logger.Debug("Sending message to connected client on topic: ", topic, " qos: ", qos)

	t := mqttClient.Publish(topic, qos, false, messageBytes)
	go func() {
		_ = t.Wait() // Can also use '<-t.Done()' in releases > 1.2.0
		if t.Error() != nil {
			logger := logger.WithFields(logrus.Fields{"error": t.Error()})
			logger.Error("Error sending a message to MQTT broker")
		}
	}()

	return nil
}
