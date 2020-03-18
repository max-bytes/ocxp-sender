package main

import "fmt"
import "net/http"
import "os"
import "encoding/json"
import "net/url"

type Configuration struct {
    TargetURL         string
}

func main() {

	args := os.Args[1:]

	if len(args) != 4 {
		fmt.Fprintf(os.Stderr, "error: wrong number of arguments\n")
		fmt.Fprintf(os.Stderr, "Usage: %v hostname service-description state pluginOutput\n", os.Args[0])
		os.Exit(1)
	}

	hostname := args[0]
	serviceDescription := args[1]
	state := args[2]
	pluginOutput := args[3]

	// read config
	var configuration Configuration
	configFilenameEnvVarName := "OCXP_SENDER_CONFIGFILE"
	configFilename := os.Getenv(configFilenameEnvVarName)
	if configFilename == "" {
		fmt.Fprintf(os.Stderr, "error: environment variable %v not set\n", configFilenameEnvVarName)
		os.Exit(1)
	}
	//configFilename := "./config/config.dev.json" // TODO
	// dat, err := ioutil.ReadFile(configFilename)
    // fmt.Print(string(dat))
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

	resp, err := http.PostForm(configuration.TargetURL, url.Values{
		"hostname": {hostname}, "serviceDescription": {serviceDescription}, "state": {state}, "pluginOutput": {pluginOutput}})

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "error: %v\n", resp.Status)
		os.Exit(1)
	}
}

