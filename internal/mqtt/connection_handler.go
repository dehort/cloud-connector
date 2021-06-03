package mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/RedHatInsights/cloud-connector/internal/config"
	"github.com/RedHatInsights/cloud-connector/internal/controller"
	"github.com/RedHatInsights/cloud-connector/internal/domain"
	"github.com/RedHatInsights/cloud-connector/internal/platform/logger"

	MQTT "github.com/eclipse/paho.mqtt.golang"
	kafka "github.com/segmentio/kafka-go"
	"github.com/sirupsen/logrus"
)

const (
	canonicalFactsKey           = "canonical_facts"
	dispatchersKey              = "dispatchers"
	catalogDispatcherKey        = "catalog"
	catalogApplicationType      = "ApplicationType"
	catalogSourceName           = "SrcName"
	catalogSourceRef            = "SourceRef"
	catalogSourceType           = "SrcType"
	playbookWorkerDispatcherKey = "rhc-worker-playbook"
)

type Subscriber struct {
	Topic      string
	EntryPoint MQTT.MessageHandler
	Qos        byte
}

func CreateBrokerConnection(brokerUrl string, onConnectHandler func(MQTT.Client), brokerConfigFuncs ...MqttClientOptionsFunc) (MQTT.Client, error) {

	return createBrokerConnection(brokerUrl, onConnectHandler, brokerConfigFuncs...)
}

func createBrokerConnection(brokerUrl string, onConnectHandler func(MQTT.Client), brokerConfigFuncs ...MqttClientOptionsFunc) (MQTT.Client, error) {

	connOpts, err := NewBrokerOptions(brokerUrl, brokerConfigFuncs...)
	if err != nil {
		logger.Log.WithFields(logrus.Fields{"error": err}).Error("Unable to build MQTT ClientOptions")
		return nil, err
	}

	connOpts.SetOnConnectHandler(onConnectHandler)

	mqttClient := MQTT.NewClient(connOpts)
	if token := mqttClient.Connect(); token.Wait() && token.Error() != nil {
		logger.Log.WithFields(logrus.Fields{"error": token.Error()}).Error("Unable to connect to MQTT broker")
		return nil, token.Error()
	}

	logger.Log.Info("Connected to MQTT broker: ", brokerUrl)

	return mqttClient, nil
}

func RegisterSubscribers(brokerUrl string, subscribers []Subscriber, defaultMessageHandler func(MQTT.Client, MQTT.Message), brokerConfigFuncs ...MqttClientOptionsFunc) (MQTT.Client, error) {

	// Add a default publish message handler as some messages will get delivered before the topic
	// subscriptions are setup completely
	// See "Common Problems" here: https://github.com/eclipse/paho.mqtt.golang#common-problems
	brokerConfigFuncs = append(brokerConfigFuncs, WithDefaultPublishHandler(defaultMessageHandler))

	return createBrokerConnection(
		brokerUrl,
		func(client MQTT.Client) {
			for _, subscriber := range subscribers {
				logger.Log.Infof("Subscribing to MQTT topic: %s - QOS: %d\n", subscriber.Topic, subscriber.Qos)
				if token := client.Subscribe(subscriber.Topic, subscriber.Qos, subscriber.EntryPoint); token.Wait() && token.Error() != nil {
					logger.Log.WithFields(logrus.Fields{"error": token.Error()}).Fatalf("Subscribing to MQTT topic (%s) failed", subscriber.Topic)
				}
			}
		},
		brokerConfigFuncs...)
}

func ControlMessageHandler(ctx context.Context, kafkaWriter *kafka.Writer, topicVerifier *TopicVerifier) func(MQTT.Client, MQTT.Message) {
	return func(client MQTT.Client, message MQTT.Message) {

		mqttMessageID := fmt.Sprintf("%d", message.MessageID())

		_, clientID, err := topicVerifier.VerifyIncomingTopic(message.Topic())
		if err != nil {
			logger.Log.WithFields(logrus.Fields{"error": err}).Error("Failed to verify topic")
			return
		}

		logger := logger.Log.WithFields(logrus.Fields{"client_id": clientID, "mqtt_message_id": mqttMessageID})

		if len(message.Payload()) == 0 {
			// This will happen when a retained message is removed
			// This can also happen when rhcd is "priming the pump" as required by the akamai broker
			logger.Trace("client sent an empty payload")
			return
		}

		go func() {
			err := kafkaWriter.WriteMessages(ctx,
				kafka.Message{
					Headers: []kafka.Header{
						{"topic", []byte(message.Topic())},
						{"mqtt_message_id", []byte(mqttMessageID)},
					}, // FIXME:  hard coded string??
					Value: message.Payload(),
				})

			logger.Debug("MQTT message written to kafka")

			if err != nil {
				logger.WithFields(logrus.Fields{"error": err}).Error("Error writing MQTT message to kafka")

				if errors.Is(err, context.Canceled) == true {
					// The context was canceled.  This likely happened due to the process shutting down,
					// so just return and allow things to shutdown cleanly
					return
				}

				// If writing to kafka fails, then just fall over and do not read anymore
				// messages from the mqtt broker
				logger.Fatal("Failed writing to kafka")
			}
		}()
	}
}

func HandleControlMessage(cfg *config.Config, mqttClient MQTT.Client, topicVerifier *TopicVerifier, topicBuilder *TopicBuilder, connectionRegistrar controller.ConnectionRegistrar, accountResolver controller.AccountIdResolver, connectedClientRecorder controller.ConnectedClientRecorder, sourcesRecorder controller.SourcesRecorder) func(MQTT.Client, string, string) {

	return func(client MQTT.Client, topic string, payload string) {
		logger.Log.Debugf("Received control message on topic: %s\nMessage: %s\n", topic, payload)

		metrics.controlMessageReceivedCounter.Inc()

		_, clientID, err := topicVerifier.VerifyIncomingTopic(topic)
		if err != nil {
			logger.Log.WithFields(logrus.Fields{"error": err}).Error("Failed to verify topic")
			return
		}

		logger := logger.Log.WithFields(logrus.Fields{"client_id": clientID})

		if len(payload) == 0 {
			// This will happen when a retained message is removed
			logger.Trace("client sent an empty payload")
			return
		}

		var controlMsg ControlMessage

		if err := json.Unmarshal([]byte(payload), &controlMsg); err != nil {
			logger.WithFields(logrus.Fields{"error": err}).Error("Failed to unmarshal control message")
			return
		}

		logger.Debug("Got a control message:", controlMsg)

		switch controlMsg.MessageType {
		case "connection-status":
			handleConnectionStatusMessage(client, clientID, controlMsg, cfg, topicBuilder, connectionRegistrar, accountResolver, connectedClientRecorder, sourcesRecorder)
		case "event":
			handleEventMessage(client, clientID, controlMsg)
		default:
			logger.Debug("Received an invalid message type:", controlMsg.MessageType)
		}
	}
}

func handleConnectionStatusMessage(client MQTT.Client, clientID domain.ClientID, msg ControlMessage, cfg *config.Config, topicBuilder *TopicBuilder, connectionRegistrar controller.ConnectionRegistrar, accountResolver controller.AccountIdResolver, connectedClientRecorder controller.ConnectedClientRecorder, sourcesRecorder controller.SourcesRecorder) error {

	logger := logger.Log.WithFields(logrus.Fields{"client_id": clientID})

	logger.Debug("handling connection status control message")

	handshakePayload := msg.Content.(map[string]interface{})

	connectionState, gotConnectionState := handshakePayload["state"]

	if gotConnectionState == false {
		// FIXME: Close down the connection
		return errors.New("Invalid connection state")
	}

	if connectionState == "online" {
		return handleOnlineMessage(client, clientID, msg, cfg, topicBuilder, accountResolver, connectionRegistrar, connectedClientRecorder, sourcesRecorder)
	} else if connectionState == "offline" {
		return handleOfflineMessage(client, clientID, msg, connectionRegistrar)
	} else {
		logger.Debug("Invalid connection state from connection-status message.")
		return errors.New("Invalid connection state")
	}

	return nil
}

func handleOnlineMessage(client MQTT.Client, clientID domain.ClientID, msg ControlMessage, cfg *config.Config, topicBuilder *TopicBuilder, accountResolver controller.AccountIdResolver, connectionRegistrar controller.ConnectionRegistrar, connectedClientRecorder controller.ConnectedClientRecorder, sourcesRecorder controller.SourcesRecorder) error {

	logger := logger.Log.WithFields(logrus.Fields{"client_id": clientID})

	logger.Debug("handling online connection-status message")

	identity, account, err := accountResolver.MapClientIdToAccountId(context.Background(), clientID)
	if err != nil {
		logger.WithFields(logrus.Fields{"error": err}).Error("Failed to resolve client id to account number")

		sendReconnectMessageToClient(client, logger, topicBuilder, cfg.MqttControlPublishQoS, clientID, "Authentication failed", cfg.InvalidHandshakeReconnectDelay)

		return err
	}

	logger = logger.WithFields(logrus.Fields{"account": account})

	handshakePayload := msg.Content.(map[string]interface{})

	rhcClient := domain.RhcClient{ClientID: clientID,
		Account:        account,
		Dispatchers:    handshakePayload[dispatchersKey],
		CanonicalFacts: handshakePayload[canonicalFactsKey],
	}

	_, err = connectionRegistrar.Register(context.Background(), rhcClient)
	if err != nil {
		sendReconnectMessageToClient(client, logger, topicBuilder, cfg.MqttControlPublishQoS, clientID, "Connection registration failed", cfg.InvalidHandshakeReconnectDelay)
		return err
	}

	if shouldHostBeRegisteredWithInventory(handshakePayload) == true {

		err = connectedClientRecorder.RecordConnectedClient(context.Background(), identity, rhcClient)

		if err != nil {
			logger.WithFields(logrus.Fields{"error": err}).Error("Failed to record client id within the platform")

			// If we cannot "register" the connection with inventory, then send a disconnect message
			sendReconnectMessageToClient(client, logger, topicBuilder, cfg.MqttControlPublishQoS, clientID, "rhc connection registration failed", cfg.InvalidHandshakeReconnectDelay)

			return err
		}
	}

	processDispatchers(sourcesRecorder, identity, account, clientID, handshakePayload)

	return nil
}

func shouldHostBeRegisteredWithInventory(handshakePayload map[string]interface{}) bool {
	return doesHostHaveCanonicalFacts(handshakePayload) && doesHostHavePlaybookWorker(handshakePayload)
}

func doesHostHaveCanonicalFacts(handshakePayload map[string]interface{}) bool {
	_, gotCanonicalFacts := handshakePayload[canonicalFactsKey]
	return gotCanonicalFacts
}

func doesHostHavePlaybookWorker(handshakePayload map[string]interface{}) bool {

	dispatchers, gotDispatchers := handshakePayload[dispatchersKey]

	if gotDispatchers == false {
		return false
	}

	dispatchersMap := dispatchers.(map[string]interface{})

	_, foundPlaybookDispatcher := dispatchersMap[playbookWorkerDispatcherKey]

	return foundPlaybookDispatcher
}

func processDispatchers(sourcesRecorder controller.SourcesRecorder, identity domain.Identity, account domain.AccountID, clientId domain.ClientID, handshakePayload map[string]interface{}) {

	logger := logger.Log.WithFields(logrus.Fields{"client_id": clientId, "account": account})

	dispatchers, gotDispatchers := handshakePayload[dispatchersKey]

	if gotDispatchers == false {
		logger.Debug("No dispatchers found")
		return
	}

	dispatchersMap := dispatchers.(map[string]interface{})

	catalog, gotCatalog := dispatchersMap[catalogDispatcherKey]

	if gotCatalog == false {
		logger.Debug("No catalog dispatcher found")
		return
	}

	catalogMap := catalog.(map[string]interface{})

	applicationType, gotApplicationType := catalogMap[catalogApplicationType]
	sourceType, gotSourceType := catalogMap[catalogSourceType]
	sourceRef, gotSourceRef := catalogMap[catalogSourceRef]
	sourceName, gotSourceName := catalogMap[catalogSourceName]

	if gotApplicationType != true || gotSourceType != true || gotSourceRef != true || gotSourceName != true {
		// MISSING FIELDS
		logger.Debug("Found a catalog dispatcher, but missing some of the required fields")
		return
	}

	err := sourcesRecorder.RegisterWithSources(identity, account, clientId, sourceRef.(string), sourceName.(string), sourceType.(string), applicationType.(string))
	if err != nil {
		logger.WithFields(logrus.Fields{"error": err}).Error("Failed to register catalog with sources")
	}
}

func handleOfflineMessage(client MQTT.Client, clientID domain.ClientID, msg ControlMessage, connectionRegistrar controller.ConnectionRegistrar) error {
	logger := logger.Log.WithFields(logrus.Fields{"client_id": clientID})

	logger.Debug("handling offline connection-status message")

	connectionRegistrar.Unregister(context.Background(), clientID)

	return nil
}

func handleEventMessage(client MQTT.Client, clientID domain.ClientID, msg ControlMessage) error {
	logger := logger.Log.WithFields(logrus.Fields{"client_id": clientID})
	logger.Debugf("Received an event message from client: %v\n", msg)
	return nil
}

func DataMessageHandler() func(MQTT.Client, MQTT.Message) {
	return func(client MQTT.Client, message MQTT.Message) {
		logger.Log.Debugf("Received data message on topic: %s\nMessage: %s\n", message.Topic(), message.Payload())

		metrics.dataMessageReceivedCounter.Inc()

		if message.Payload() == nil || len(message.Payload()) == 0 {
			logger.Log.Trace("Received empty data message")
			return
		}
	}
}

func DefaultMessageHandler(topicVerifier *TopicVerifier, controlMessageHandler, dataMessageHandler func(MQTT.Client, MQTT.Message)) func(client MQTT.Client, message MQTT.Message) {
	return func(client MQTT.Client, message MQTT.Message) {
		logger.Log.Debugf("Received message on topic: %s\nMessage: %s\n", message.Topic(), message.Payload())

		topicType, _, err := topicVerifier.VerifyIncomingTopic(message.Topic())

		if err != nil {
			logger.Log.Debugf("Topic verification failed : %s\nMessage: %s\n", message.Topic(), message.Payload())
			return
		}

		if topicType == ControlTopicType {
			controlMessageHandler(client, message)
		} else if topicType == DataTopicType {
			dataMessageHandler(client, message)
		} else {
			logger.Log.Debugf("Received message on unknown topic: %s\nMessage: %s\n", message.Topic(), message.Payload())
		}
	}
}
