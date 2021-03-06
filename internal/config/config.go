package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

const (
	ENV_PREFIX = "CLOUD_CONNECTOR"

	HTTP_SHUTDOWN_TIMEOUT          = "HTTP_Shutdown_Timeout"
	SERVICE_TO_SERVICE_CREDENTIALS = "Service_To_Service_Credentials"
	PROFILE                        = "Enable_Profile"
	BROKERS                        = "Kafka_Brokers"
	JOBS_TOPIC                     = "Kafka_Jobs_Topic"
	JOBS_GROUP_ID                  = "Kafka_Jobs_Group_Id"
	RESPONSES_TOPIC                = "Kafka_Responses_Topic"
	RESPONSES_BATCH_SIZE           = "Kafka_Responses_Batch_Size"
	RESPONSES_BATCH_BYTES          = "Kafka_Responses_Batch_Bytes"
	DEFAULT_BROKER_ADDRESS         = "kafka:29092"
)

type Config struct {
	HttpShutdownTimeout         time.Duration
	ServiceToServiceCredentials map[string]interface{}
	Profile                     bool
	KafkaBrokers                []string
	KafkaJobsTopic              string
	KafkaResponsesTopic         string
	KafkaResponsesBatchSize     int
	KafkaResponsesBatchBytes    int
	KafkaGroupID                string
}

func (c Config) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s\n", HTTP_SHUTDOWN_TIMEOUT, c.HttpShutdownTimeout)
	fmt.Fprintf(&b, "%s: %t\n", PROFILE, c.Profile)
	fmt.Fprintf(&b, "%s: %s\n", BROKERS, c.KafkaBrokers)
	fmt.Fprintf(&b, "%s: %s\n", JOBS_TOPIC, c.KafkaJobsTopic)
	fmt.Fprintf(&b, "%s: %s\n", RESPONSES_TOPIC, c.KafkaResponsesTopic)
	fmt.Fprintf(&b, "%s: %d\n", RESPONSES_BATCH_SIZE, c.KafkaResponsesBatchSize)
	fmt.Fprintf(&b, "%s: %d\n", RESPONSES_BATCH_BYTES, c.KafkaResponsesBatchBytes)
	fmt.Fprintf(&b, "%s: %s\n", JOBS_GROUP_ID, c.KafkaGroupID)
	return b.String()
}

func GetConfig() *Config {
	options := viper.New()

	options.SetDefault(HTTP_SHUTDOWN_TIMEOUT, 2)
	options.SetDefault(SERVICE_TO_SERVICE_CREDENTIALS, "")
	options.SetDefault(PROFILE, false)
	options.SetDefault(BROKERS, []string{DEFAULT_BROKER_ADDRESS})
	options.SetDefault(JOBS_TOPIC, "platform.receptor-controller.jobs")
	options.SetDefault(RESPONSES_TOPIC, "platform.receptor-controller.responses")
	options.SetDefault(RESPONSES_BATCH_SIZE, 100)
	options.SetDefault(RESPONSES_BATCH_BYTES, 1048576)
	options.SetDefault(JOBS_GROUP_ID, "cloud-connector-consumer")
	options.SetEnvPrefix(ENV_PREFIX)
	options.AutomaticEnv()

	return &Config{
		HttpShutdownTimeout:         options.GetDuration(HTTP_SHUTDOWN_TIMEOUT) * time.Second,
		ServiceToServiceCredentials: options.GetStringMap(SERVICE_TO_SERVICE_CREDENTIALS),
		Profile:                     options.GetBool(PROFILE),
		KafkaBrokers:                options.GetStringSlice(BROKERS),
		KafkaJobsTopic:              options.GetString(JOBS_TOPIC),
		KafkaResponsesTopic:         options.GetString(RESPONSES_TOPIC),
		KafkaResponsesBatchSize:     options.GetInt(RESPONSES_BATCH_SIZE),
		KafkaResponsesBatchBytes:    options.GetInt(RESPONSES_BATCH_BYTES),
		KafkaGroupID:                options.GetString(JOBS_GROUP_ID),
	}
}
