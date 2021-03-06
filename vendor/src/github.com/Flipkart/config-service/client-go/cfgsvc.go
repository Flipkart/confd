// Config client. Also talks to coordinator for watches and versions.
//Typical use case is to get a dynamic bucket and use it to read configuration.
//The dynamic bucket is auto-updated.
//
//Sample usage:
//
// Create client instance with 50 as the size of LRU cache
//      client := cfgsvc.NewConfigServiceClient("http://localhost:8080", 50)
//
//
// get key and check its value
//  if flag := client.GetDynamicBucket("mybucket").GetBool("myflag"); flag {
//      do stuff
//  }
//
//
// If you do not wish to remember the bucket name in runtime, you can
// use the bucket struct directly, it will be auto-updated by client.
//  bucket := client.GetDynamicBucket("mybucket");
//
//
//  endpoint := bucket.GetString("endpoint");
package cfgsvc

import (
	"encoding/json"
	"errors"
	"github.com/hashicorp/golang-lru"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
	"strings"
	"fmt"
)

// ConfigServiceClient provides API to interact with config service to
// read and watch for configuration changes
type ConfigServiceClient struct {
	httpClient         *HttpClient
	instanceMetadata   *InstanceMetadata
	dynamicBucketCache *lru.Cache
	staticBucketCache  *lru.Cache
	mutex              sync.Mutex
}

type InstanceMetadata struct {
	App           string `json:"app"`
	Zone          string `json:"zone"`
	InstanceGroup string `json:"instance_group"`
	Hostname      string `json:"hostname"`
	PrimaryIP     string `json:"primary_ip"`
	Id 			  string `json:"id"`
	Vpc 		  string `json:"vpc_name"`
	VpcSubnet     string `json:"vpc_subnet_name"`
}

type CfgSvcApiOverrides struct {
	Endpoint      string
}

const InstanceMetadataFile = "/etc/default/megh/instance_metadata.json"
const DefaultZone = "in-mumbai-preprod"
const CfgSvcApiOverridesFile = "/etc/default/cfg-api"
const CloudCliEndpoint = "http://10.47.255.6:8080"

var instVpcToCfgSvc = map[string]string{
	"fk-helios": "http://10.47.7.149",
	"fk-preprod": "http://10.85.42.8",
}

var instZoneToCfgsvc = map[string]string{
	"in-mumbai-prod":    "http://10.85.50.3",
	"in-mumbai-preprod":    "http://10.85.42.8",
	"in-mumbai-preprod-b":    "http://10.85.42.8",
	"in-mumbai-gateway": "http://10.85.50.3",
	"in-chennai-1":      "http://10.47.0.101",
	"in-hyderabad-1": "http://10.24.0.32",
}

// var skipListForVpcCheck = [...]string{"in-mumbai-preprod", "in-mumbai-preprod-b", "in-mumbai-prod", "in-mumbai-gateway", "#NULL#"}

const LATEST_VERSION = -1

// NewConfigServiceClient creates a new instance of config service client and returns its pointer.
func NewConfigServiceClient(cacheSize int) (*ConfigServiceClient, error) {

	client := &ConfigServiceClient{}

	// get instance metadata
	meta := readInstMetadata()

	netHttpClient := &http.Client{Timeout: time.Duration(60 * time.Second)}

	// get url
	url := ""
	ok := false

	overrides, err := getOverrides(CfgSvcApiOverridesFile)
	if err == nil && len(overrides.Endpoint) > 0 {
		log.Println("Overriding endpoint")
		url = overrides.Endpoint
	} else {
		log.Println("Attempting to get endpoint for vpc " + meta.Vpc)
		vpc := strings.ToLower(meta.Vpc)
		if url, ok = instVpcToCfgSvc[vpc]; !ok {
			log.Println("Attempting to get endpoint for zone " + meta.Zone)
			if url, ok = instZoneToCfgsvc[meta.Zone]; !ok {
				log.Println("Instance zone not found, defaulting to " + DefaultZone)
				url = instZoneToCfgsvc[DefaultZone]
			}
		}
	}
	log.Println("Using endpoint: " + url)

	// create client
	httpClient, err := NewHttpClient(netHttpClient, url, meta)
	if err != nil {
		return nil, err
	}

	// dynamic cache
	client.dynamicBucketCache, err = lru.NewWithEvict(cacheSize, func(bucketName interface{}, value interface{}) {
		dynamicBucket := value.(*DynamicBucket)
		log.Println("Removing bucket from local cache: ", bucketName)
		dynamicBucket.Disconnected(errors.New("Bucket evicted from cache, please fetch it again"))
		dynamicBucket.shutdown()
	})

	// static cache
	client.staticBucketCache, err = lru.NewWithEvict(cacheSize, func(bucketName interface{}, value interface{}) {
		log.Println("Removing bucket from local cache: ", bucketName)
	})

	if err != nil {
		return nil, err
	} else {
		client.httpClient = httpClient
		return client, nil
	}
}

//Get a dynamic bucket which is auto-updated by a setting watch.
//Keeps a local reference of the static bucket for updating and caching.
func (this *ConfigServiceClient) GetDynamicBucket(name string) (*DynamicBucket, error) {
	if val, ok := this.dynamicBucketCache.Get(name); ok {
		dynamicBucket := val.(*DynamicBucket)
		return dynamicBucket, nil
	} else {
		//Use mutex to ensure the bucket will be fetched only once!
		this.mutex.Lock()
		defer this.mutex.Unlock()

		//Check cache again to see if the another thread has
		//already initialized the bucket
		if val, ok := this.dynamicBucketCache.Get(name); ok {
			dynamicBucket := val.(*DynamicBucket)
			return dynamicBucket, nil
		} else {
			// Initialize the bucket if this the first time
			return this.initDynamicBucket(name)
		}
	}
}

//Initialises a dynamic bucket given the bucket name
func (this *ConfigServiceClient) initDynamicBucket(name string) (*DynamicBucket, error) {
	log.Println("Initializing Config bucket: " + name)

	dynamicBucket := &DynamicBucket{httpClient: this.httpClient}

	err := ValidateBucketName(name)
	if err != nil {
		return nil, err
	}

	err = dynamicBucket.init(name)

	if err != nil {
		log.Println("Error fetching bucket: ", err)
		return nil, err
	} else {
		this.dynamicBucketCache.Add(name, dynamicBucket)
		go this.httpClient.WatchBucket(name, this.dynamicBucketCache, dynamicBucket)
		return dynamicBucket, nil
	}
}

//Get a bucket with given version. It does not set any watches.
func (this *ConfigServiceClient) GetBucket(name string, version int) (*Bucket, error) {
	if val, ok := this.staticBucketCache.Get(cacheKey(name, version)); ok {
		bucket := val.(*Bucket)
		return bucket, nil
	} else {
		//Use mutex to ensure the bucket will be fetched only once!
		this.mutex.Lock()
		defer this.mutex.Unlock()

		//Check cache again to see if the another thread has
		//already initialized the bucket
		if val, ok := this.staticBucketCache.Get(cacheKey(name, version)); ok {
			bucket := val.(*Bucket)
			return bucket, nil
		} else {
			// Initialize the bucket if this the first time
			return this.initStaticBucket(name, version)
		}
	}
}

//Initialises a bucket with given version. It does not set any watches.
func (this *ConfigServiceClient) initStaticBucket(name string, version int) (*Bucket, error) {
	log.Println("Initializing Config bucket: " + name)

	err := ValidateBucketName(name)
	if err != nil {
		return nil, err
	}
	bucket, err := this.httpClient.GetBucket(name, version)
	if err != nil {
		log.Println("Error fetching bucket: ", err)
		return nil, err
	} else {
		this.staticBucketCache.Add(cacheKey(name, version), bucket)
		return bucket, nil
	}
}

func cacheKey(name string, version int) string {
	return name + ":" + strconv.Itoa(version)
}

// func skipVpcCheck(zone string) bool {
// 	for _, z := range skipListForVpcCheck {
//         if z == zone {
//             return true
//         }
//     }
//     return false
// }

func getProperties(fileName string) (map[string]string, error) {
	bytes, err := ioutil.ReadFile(fileName)

	if err != nil {
		log.Println("Failed to read file: " + fileName + ". Ignoring overrides")
		return nil, err
	}

	lines := strings.Split(string(bytes[:]), "\n")

	properties := map[string]string{}
	for _, line := range lines {
		if len(line) > 0 {
			kv := strings.Split(line, "=")
			if len(kv) != 2 {
				return nil, fmt.Errorf("format error in line : \"%s\"", line)
			}

			key := strings.TrimSpace(kv[0])
			value := strings.TrimSpace(kv[1])

			if len(key) == 0 || len(value) == 0 {
				return nil, fmt.Errorf("format error in line : \"%s\"", line)
			}

			properties[key] = value
		}
	} 

	return properties, nil
}

func getOverrides(fileName string) (CfgSvcApiOverrides, error) {
	overrides := CfgSvcApiOverrides{Endpoint : ""}

	properties, err := getProperties(fileName)

	if err != nil {
		return overrides, err
	}

	host, ok := properties["host"]
	if !ok {
		return overrides, fmt.Errorf("empty overrides")  
	}

	port_str, ok := properties["port"]

	if !ok {
		port_str = "80"
	} else {
		_, err = strconv.Atoi(port_str)
		if err != nil {
			return overrides, fmt.Errorf("port is not a number") 
		}	
	}
	
	overrides.Endpoint = "http://" + host + ":" + port_str

	return overrides, nil
}

// func doRequest(httpClient *http.Client, url string) ([]byte, error) {
// 	req, _ := http.NewRequest("GET", url, nil)
// 	req.Header.Add("Accept", "application/json")

// 	resp, err := httpClient.Do(req)

// 	if err != nil {
// 		log.Println("Failed to do request. error: " + err.Error())
// 		return nil, err
// 	}

// 	defer resp.Body.Close()

// 	return ioutil.ReadAll(resp.Body)
// }

// func getVpcSubnetName(httpClient *http.Client, meta *InstanceMetadata) (string, error) {
	
// 	url := CloudCliEndpoint + "/compute/v2/apps/" + meta.App + "/zones/" + meta.Zone + "/instances/" + meta.Id

// 	resp_body, err := doRequest(httpClient, url)

// 	if err != nil {
// 		return "", err
// 	}

// 	var jsonVal map[string]interface{}

// 	if err := json.Unmarshal(resp_body, &jsonVal); err != nil {
//         log.Println("Error parsing cloud cli rsponse as json. error: " + err.Error())
//         return "", err
//     }

//     vpcname := jsonVal["vpc_subnet_name"]
//     if vpcname != nil {
// 		return strings.ToLower(vpcname.(string)), nil 
//     }

//     return "", fmt.Errorf("vpc name not found")
// }

func readInstMetadata() *InstanceMetadata {

	// create instance
	meta := &InstanceMetadata{}

	// read from json
	jsn, err := os.Open(InstanceMetadataFile)
	if err != nil {
		log.Println("Error opening " + InstanceMetadataFile + ": " + err.Error())
	}

	// parse json
	jsonParser := json.NewDecoder(jsn)
	if err = jsonParser.Decode(&meta); err != nil {
		log.Println("Error parsing instance metadata: " + err.Error())
	}

	// get hostname
	if meta.Hostname, err = os.Hostname(); err != nil {
		log.Println("Error getting hostname, using from metadata (" + meta.Hostname + "): " + err.Error())
	}

	// get ipv4
	if meta.PrimaryIP == "" {
		interfaces, _ := net.Interfaces()
		for _, inter := range interfaces {
			if addrs, err := inter.Addrs(); err == nil {
				for _, addr := range addrs {
					switch ip := addr.(type) {
					case *net.IPNet:
						if ip.IP.DefaultMask() != nil && !ip.IP.IsLoopback() {
							meta.PrimaryIP = ip.IP.To4().String()
						}
					}
				}
			}
		}
	}

	// defaults
	if meta.Zone == "" {
		meta.InstanceGroup = "#NULL#"
	}
	if meta.App == "" {
		meta.App = "#NULL#"
	}
	if meta.InstanceGroup == "" {
		meta.InstanceGroup = "#NULL#"
	}
	if meta.Vpc == "" {
		meta.Vpc = "#NULL#"
	}
	if meta.VpcSubnet == "" {
		meta.VpcSubnet = "#NULL#"
	}
	return meta
}
