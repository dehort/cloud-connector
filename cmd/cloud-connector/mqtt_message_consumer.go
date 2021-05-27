package main

import (
	"context"
	"crypto/tls"
	"errors"
	"net/url"
	"os"
	"os/signal"
	"syscall"

	"github.com/RedHatInsights/cloud-connector/internal/config"
	"github.com/RedHatInsights/cloud-connector/internal/controller"
	"github.com/RedHatInsights/cloud-connector/internal/controller/api"
	"github.com/RedHatInsights/cloud-connector/internal/mqtt"
	"github.com/RedHatInsights/cloud-connector/internal/platform/logger"
	"github.com/RedHatInsights/cloud-connector/internal/platform/utils"
	"github.com/RedHatInsights/cloud-connector/internal/platform/utils/jwt_utils"
	"github.com/RedHatInsights/cloud-connector/internal/platform/utils/tls_utils"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

func buildMessageHandlerMqttBrokerConfigFuncList(brokerUrl string, tlsConfig *tls.Config, cfg *config.Config) ([]mqtt.MqttClientOptionsFunc, error) {

	u, err := url.Parse(brokerUrl)
	if err != nil {
		logger.Log.WithFields(logrus.Fields{"error": err}).Error("Unable to determine protocol for the MQTT connection")
		return nil, err
	}

	brokerConfigFuncs := []mqtt.MqttClientOptionsFunc{}

	if tlsConfig != nil {
		brokerConfigFuncs = append(brokerConfigFuncs, mqtt.WithTlsConfig(tlsConfig))
	}

	if u.Scheme == "wss" { //Rethink this check - jwt also works over TLS
		jwtGenerator, err := jwt_utils.NewJwtGenerator(cfg.MqttBrokerJwtGeneratorImpl, cfg)
		if err != nil {
			logger.Log.WithFields(logrus.Fields{"error": err}).Error("Unable to instantiate a JWT generator for the MQTT connection")
			return nil, err
		}
		brokerConfigFuncs = append(brokerConfigFuncs, mqtt.WithJwtAsHttpHeader(jwtGenerator))
		brokerConfigFuncs = append(brokerConfigFuncs, mqtt.WithJwtReconnectingHandler(jwtGenerator))
	}

	if cfg.MqttClientId != "" {
		brokerConfigFuncs = append(brokerConfigFuncs, mqtt.WithClientID(cfg.MqttClientId))
	}

	brokerConfigFuncs = append(brokerConfigFuncs, mqtt.WithCleanSession(cfg.MqttCleanSession))

	brokerConfigFuncs = append(brokerConfigFuncs, mqtt.WithResumeSubs(cfg.MqttResumeSubs))

	return brokerConfigFuncs, nil
}

func startMqttMessageConsumer(mgmtAddr string) {

	logger.InitLogger()

	logger.Log.Info("Starting Cloud-Connector service")

	cfg := config.GetConfig()
	logger.Log.Info("Cloud-Connector configuration:\n", cfg)

	tlsConfigFuncs, err := buildBrokerTlsConfigFuncList(cfg)
	if err != nil {
		logger.LogFatalError("TLS configuration error for MQTT Broker connection", err)
	}

	tlsConfig, err := tls_utils.NewTlsConfig(tlsConfigFuncs...)
	if err != nil {
		logger.LogFatalError("Unable to configure TLS for MQTT Broker connection", err)
	}

	sqlConnectionRegistrar, err := controller.NewSqlConnectionRegistrar(cfg)
	if err != nil {
		logger.LogFatalError("Failed to create SQL Connection Registrar", err)
	}

	accountResolver, err := controller.NewAccountIdResolver(cfg.ClientIdToAccountIdImpl, cfg)
	if err != nil {
		logger.LogFatalError("Failed to create Account ID Resolver", err)
	}

	connectedClientRecorder, err := controller.NewConnectedClientRecorder(cfg.ConnectedClientRecorderImpl, cfg)
	if err != nil {
		logger.LogFatalError("Failed to create Connected Client Recorder", err)
	}

	sourcesRecorder, err := controller.NewSourcesRecorder(cfg.SourcesRecorderImpl, cfg)
	if err != nil {
		logger.LogFatalError("Failed to create Sources Recorder", err)
	}

	mqttTopicBuilder := mqtt.NewTopicBuilder(cfg.MqttTopicPrefix)
	mqttTopicVerifier := mqtt.NewTopicVerifier(cfg.MqttTopicPrefix)

	controlMsgHandler := mqtt.ControlMessageHandler(cfg, mqttTopicVerifier, mqttTopicBuilder, sqlConnectionRegistrar, accountResolver, connectedClientRecorder, sourcesRecorder)
	dataMsgHandler := mqtt.DataMessageHandler()

	defaultMsgHandler := mqtt.DefaultMessageHandler(mqttTopicVerifier, controlMsgHandler, dataMsgHandler)

	subscribers := []mqtt.Subscriber{
		mqtt.Subscriber{
			Topic:      mqttTopicBuilder.BuildIncomingWildcardControlTopic(),
			EntryPoint: controlMsgHandler,
			Qos:        cfg.MqttControlSubscriptionQoS,
		},
		mqtt.Subscriber{
			Topic:      mqttTopicBuilder.BuildIncomingWildcardDataTopic(),
			EntryPoint: dataMsgHandler,
			Qos:        cfg.MqttDataSubscriptionQoS,
		},
	}

	brokerOptions, err := buildMessageHandlerMqttBrokerConfigFuncList(cfg.MqttBrokerAddress, tlsConfig, cfg)
	if err != nil {
		logger.LogFatalError("Unable to configure MQTT Broker connection", err)
	}

	_, err = mqtt.RegisterSubscribers(cfg.MqttBrokerAddress, subscribers, defaultMsgHandler, brokerOptions...)
	if err != nil {
		logger.LogFatalError("Failed to connect to MQTT broker", err)
	}

	apiMux := mux.NewRouter()

	monitoringServer := api.NewMonitoringServer(apiMux, cfg)
	monitoringServer.Routes()

	apiSrv := utils.StartHTTPServer(mgmtAddr, "management", apiMux)

	signalChan := make(chan os.Signal, 1)

	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)

	sig := <-signalChan
	logger.Log.Info("Received signal to shutdown: ", sig)

	ctx, cancel := context.WithTimeout(context.Background(), cfg.HttpShutdownTimeout)
	defer cancel()

	utils.ShutdownHTTPServer(ctx, "management", apiSrv)

	logger.Log.Info("Cloud-Connector shutting down")
}

func buildBrokerTlsConfigFuncList(cfg *config.Config) ([]tls_utils.TlsConfigFunc, error) {

	tlsConfigFuncs := []tls_utils.TlsConfigFunc{}

	if cfg.MqttBrokerTlsCertFile != "" && cfg.MqttBrokerTlsKeyFile != "" {
		tlsConfigFuncs = append(tlsConfigFuncs, tls_utils.WithCert(cfg.MqttBrokerTlsCertFile, cfg.MqttBrokerTlsKeyFile))
	} else if cfg.MqttBrokerTlsCertFile != "" || cfg.MqttBrokerTlsKeyFile != "" {
		return tlsConfigFuncs, errors.New("Cert or key file specified without the other")
	}

	if cfg.MqttBrokerTlsCACertFile != "" {
		tlsConfigFuncs = append(tlsConfigFuncs, tls_utils.WithCACerts(cfg.MqttBrokerTlsCACertFile))
	}

	if cfg.MqttBrokerTlsSkipVerify == true {
		tlsConfigFuncs = append(tlsConfigFuncs, tls_utils.WithSkipVerify())
	}

	return tlsConfigFuncs, nil
}