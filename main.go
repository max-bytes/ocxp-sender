package main

import (
	"fmt"
	"os"
	"bytes"
	"strconv"
	"time"
	"encoding/json"
	"crypto/tls"
	"regexp"
	MQTT "github.com/eclipse/paho.mqtt.golang"
	"github.com/influxdata/line-protocol"
)

const Topic = "naemon/metrics"

type Configuration struct {
    MQTTServerURL string
}

type Metric struct {
	name string
	tags []*protocol.Tag
	fields []*protocol.Field
}
func (m Metric) Time() time.Time {
    return time.Now()
}
func (m Metric) Name() string {
    return m.name
}
func (m Metric) TagList() []*protocol.Tag {
    return m.tags
}
func (m Metric) FieldList() []*protocol.Field {
    return m.fields
}

func main() {

	args := os.Args[1:]
	if len(args) != 4 {
		fmt.Fprintf(os.Stderr, "error: wrong number of arguments\n")
		fmt.Fprintf(os.Stderr, "Usage: %v hostname service-description check-state-id perfData\n", os.Args[0])
		os.Exit(1)
	}
	hostname := args[0]
	serviceDescription := args[1]
	stateStr := args[2]
	state, err := strconv.Atoi(stateStr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Could not parse passed check-state-id \"%v\" to integer", stateStr)
		os.Exit(1)
	}
	perfData := args[3]
	// read config
	var configuration Configuration
	configFilenameEnvVarName := "OCXP_SENDER_CONFIGFILE"
	configFilename := os.Getenv(configFilenameEnvVarName)
	if configFilename == "" {
		fmt.Fprintf(os.Stderr, "error: environment variable %v not set\n", configFilenameEnvVarName)
		os.Exit(1)
	}
	file, err := os.Open(configFilename)
	if err != nil { 
		fmt.Fprintf(os.Stderr, "error: could not open config file %v: %v\n", configFilename, err)
		os.Exit(1)
	}
	decoder := json.NewDecoder(file) 
	err = decoder.Decode(&configuration) 
	if err != nil { 
		fmt.Fprintf(os.Stderr, "error: could not decode config file %v: %v\n", configFilename, err)
		os.Exit(1)
	}

	var b bytes.Buffer
	encoder := protocol.NewEncoder(&b)

	for pd := range parsePerfData(perfData) {
		metric := perfData2metric(pd, map[string]string {
			"host": hostname,
			"servicedesc": serviceDescription,
		})
		
		_, err = encoder.Encode(metric)
		if err != nil {
			fmt.Println(err)
		}
	}

	// TODO: add lots of tags
	metric := perfData2metric(PerfData{
		Key: "state",
		Value: float64(state), // in influxDB, values are floats, convert from integer to float64
	}, map[string]string {
		"host": hostname,
		"servicedesc": serviceDescription,
	})
	
	_, err = encoder.Encode(metric)
	if err != nil {
		fmt.Println(err)
	}

	// TODO: add state as its own metric(?), with the state encoding as an integer (-1 to 2 or 0 to 3?)

	// only publish if there are actually metrics/perfdata
	if b.Len() > 0 {
		clientid := "" // empty client id, should result in a stateless client
		topic := Topic
		qos := 1 // NOTE: we use qos=1, which does NOT prevent duplicates, but is faster than qos=2; see https://www.hivemq.com/blog/mqtt-essentials-part-6-mqtt-quality-of-service-levels/
		retained := false

		connOpts := MQTT.NewClientOptions().AddBroker(configuration.MQTTServerURL).SetClientID(clientid).SetCleanSession(true)
		tlsConfig := &tls.Config{InsecureSkipVerify: true, ClientAuth: tls.NoClientCert}
		connOpts.SetTLSConfig(tlsConfig)

		client := MQTT.NewClient(connOpts)
		if token := client.Connect(); token.Wait() && token.Error() != nil {
			fmt.Println(token.Error())
			os.Exit(1)
		}

		if token := client.Publish(topic, byte(qos), retained, b.String()); token.Wait() && token.Error() != nil {
			fmt.Println(token.Error())
			os.Exit(1)
		}
		client.Disconnect(100)
	}
}

func perfData2metric(pd PerfData, addedTags map[string]string) Metric {
	var fields = []*protocol.Field {
		&(protocol.Field { Key: "value", Value: pd.Value }),
	}
	if pd.Warn != nil {
		fields = append(fields, &(protocol.Field { Key: "warn", Value: *pd.Warn }))
	}
	if pd.Crit != nil {
		fields = append(fields, &(protocol.Field { Key: "crit", Value: *pd.Crit }))
	}
	if pd.Min != nil {
		fields = append(fields, &(protocol.Field { Key: "min", Value: *pd.Min }))
	}
	if pd.Max != nil {
		fields = append(fields, &(protocol.Field { Key: "max", Value: *pd.Max }))
	}

	var tags = []*protocol.Tag {
		&(protocol.Tag { Key: "label", Value: pd.Key }),
	}
	for key, value := range addedTags {
		tags = append(tags, &(protocol.Tag { Key: key, Value: value }))
	}

	// add UOM, if present
	if pd.UOM != nil {
		tags = append(tags, &(protocol.Tag { Key: "uom", Value: *pd.UOM }))
	}

	metric := Metric {
		name: "value",
		fields: fields,
		tags: tags,
	}

	return metric
}

type PerfData struct {
	Key string
	Value interface{}
	UOM *string
	Warn *float64
	Crit *float64
	Min *float64
	Max *float64
}

// partly taken from https://github.com/Griesbacher/nagflux/blob/ea877539bc49ed67e9a5e35b8a127b1ff4cadaad/collector/spoolfile/nagiosSpoolfileWorker.go
var regexPerformanceLabel = regexp.MustCompile(`([^=]+)=(U|[\d\.,\-]+)([\pL\/%]*);?([\d\.,\-:~@]+)?;?([\d\.,\-:~@]+)?;?([\d\.,\-]+)?;?([\d\.,\-]+)?;?\s*`)
func parsePerfData(str string) <-chan PerfData {
	ch := make(chan PerfData)
	go func() {
		perfSlice := regexPerformanceLabel.FindAllStringSubmatch(str, -1)

		for _, value := range perfSlice {
			v, err := strconv.ParseFloat(value[2], 64)
			if err != nil {
				fmt.Println(err)
				continue
			}
			var uom *string = nil
			if value[3] != "" {
				uom = &(value[3])
			}
			var warn *float64 = nil
			warnF, err := strconv.ParseFloat(value[4], 64)
			if err == nil {
				warn = &warnF
			}
			var crit *float64 = nil
			critF, err := strconv.ParseFloat(value[5], 64)
			if err == nil {
				crit = &critF
			}
			var min *float64 = nil
			minF, err := strconv.ParseFloat(value[6], 64)
			if err == nil {
				min = &minF
			}
			var max *float64 = nil
			maxF, err := strconv.ParseFloat(value[7], 64)
			if err == nil {
				max = &maxF
			}
			perf := PerfData{
				Key: value[1],
				Value: v,
				UOM: uom,
				Warn: warn,
				Crit: crit,
				Min: min,
				Max: max,
			}
			ch <- perf
		}
		close(ch)
	}()

	return ch
}