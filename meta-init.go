package main

import (
	"path/filepath"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"net/http"
	"io"
	"encoding/json"
	"io/ioutil"
	"runtime"
	"reflect"
	"strings"
	"sort"
)

var (
	displayVersion = flag.Bool("version", false, "display version")
	metaFile = flag.String("meta-file", "", "Metadata file path")
	metaDirectory = flag.String("meta-directory", "", "Metadata directory path")
)

const VERSION = "0.1.0"

type Hash map[string]interface{}

func Version() string {
	return fmt.Sprintf("meta-init v%s (built w/%s)", VERSION, runtime.Version())
}

func init() {
}

func main() {
	var metadataContent []byte
	var errChk error
	var metaface interface{}

	flag.Parse()

	if *displayVersion {
		fmt.Printf("%s", Version())
		return
	}

	if *metaFile != "" {
		content, errChk := ioutil.ReadFile(*metaFile)
		if errChk != nil {
			fmt.Printf("ERROR: Failed to read metadata file (%s)\n", *metaFile)
			fmt.Printf(" *** %s\n", errChk.Error())
			return
		}
		metadataContent = append(metadataContent, content...)
	} else if *metaDirectory != "" {
		items, errChk := ioutil.ReadDir(*metaDirectory)
		if errChk != nil {
			fmt.Printf("ERROR: Failed to read data directory (%s)\n", *metaDirectory)
			fmt.Printf(" *** %s\n", errChk.Error())
			return
		}
		for _, item := range items {
			filePath := *metaDirectory + "/" + item.Name()
			content, errChk := ioutil.ReadFile(filePath)
			if errChk != nil {
				fmt.Printf("ERROR: Failed to read metadata file (%s)\n", filePath)
				fmt.Printf(" *** %s\n", errChk.Error())
				return
			}
			metadataContent = append(metadataContent, content...)
		}
	} else {
		fmt.Printf("ERROR: Metadata file or directory of parts is required!\n")
		return
	}

	errChk = json.Unmarshal(metadataContent, &metaface)

	if errChk != nil {
		fmt.Printf("ERROR: Failed to load JSON metadata!\n")
		fmt.Printf(" *** %s\n", errChk.Error())
	}

	topLevel := Hash(metaface.(map[string]interface{}))
	meta := unpackHash("AWS::CloudFormation::Init", topLevel)
	config := unpackHash("config", meta)

	if config != nil {
		fmt.Printf("Processing config section... \n")
		_ = processConfig(config)
		fmt.Printf("  ----> [config section] DONE\n")
	}

	commands := unpackHash("commands", config)

	if commands != nil {
		fmt.Printf("Processing commands section... \n")
		_ = processCommands(commands)
		fmt.Printf("  ----> [commands section] DONE\n")
	}


}

func processConfig(config Hash) bool {
	files := unpackHash("files", config)
	if files != nil {
		_ = processFiles(files)
	}
	return true
}

// still needs chmod and chown
func processFiles(files Hash) bool {
	var errChk error
	for fileName := range files {
		dir := filepath.Dir(fileName)
		errChk = os.MkdirAll(dir, 0755)
		if errChk != nil {
			return false
		}
		fileInfo := unpackHash(fileName, files)
		if fileInfo["content"] != nil {
			content := reflect.ValueOf(fileInfo["content"])
			kind := content.Kind().String()
			if kind == "string" {
				errChk = ioutil.WriteFile(fileName, []byte(content.String()), 0600)
			} else if kind == "map" {
				mard, _ := json.Marshal(fileInfo["content"])
				errChk = ioutil.WriteFile(fileName, mard, 0600)
			} else {
				fmt.Printf("WARN: Unknown value type. Cannot process - %s\n", kind)
			}
			if errChk != nil {
				return false
			}
		} else if fileInfo["source"] != nil {
			out, _ := os.Create(fileName)
			defer out.Close()
			resp, _ := http.Get(strings.Replace(fileInfo["source"].(string), "\"", "", -1))
			defer resp.Body.Close()
			io.Copy(out, resp.Body)
		}
	}
	return true
}

func processCommands(commands Hash) bool {
	process := true

	commandIdentifiers := make([]string, len(commands))
	i := 0
	for k, _ := range commands {
		commandIdentifiers[i] = k
		i++
	}

	sort.Strings(commandIdentifiers)

	for _, commandIdentifier := range commandIdentifiers {
		process = true

		commandInfo := unpackHash(commandIdentifier, commands)
		test := commandInfo["test"]

		if test != nil {
			process = applyCommand(reflect.ValueOf(test))
		}

		if process {
			if !applyCommand(reflect.ValueOf(commands[commandIdentifier])) {
				fmt.Printf("ERROR: Command failed [%s]\n", commandIdentifier)
				return false
			}
		} else {
			fmt.Printf("WARN: Command skipped due to test result [%s]\n", commandIdentifier)
		}
	}

	return true
}

func applyCommand(thing reflect.Value) bool {
	var errChk error

	kind := thing.Kind().String()
	if kind == "string" {
		cmd := exec.Command("sh", "-c", thing.String())
		errChk = cmd.Run()
		if errChk != nil {
			fmt.Printf("WARN: Command unsuccessful: %s\n", thing.String())
			fmt.Printf("WARN: Command returned failure: %s\n", errChk.Error())
			return false
		}
	} else if kind == "map" {
		hash := Hash(thing.Interface().(map[string]interface{}))
		command := hash["command"].(string)
		cmd := exec.Command("sh", "-c", command)
		env_map := unpackHash("env", hash)
		if env_map != nil {
			env := []string{}
			for env_name := range env_map {
				new_val := env_name + "=" + env_map[env_name].(string)
				env = append(env, new_val)
			}
			cmd.Env = env
		}
		if hash["cwd"] != nil {
			cmd.Dir = hash["cwd"].(string)
		}
		errChk = cmd.Run()
		if errChk != nil {
			fmt.Printf("WARN: Command unsuccessful: %s\n", command)
			fmt.Printf("WARN: Command returned failure: %s\n", errChk.Error())
			return false
		}
	} else {
		fmt.Printf("WARN: Unknown command type. Cannot process - %s\n", kind)
		return false
	}
	return true
}

func unpackHash(key string, hash Hash) Hash {
	if hash[key] != nil {
		return Hash(hash[key].(map[string]interface{}))
	}
	return nil
}
