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
    "log"
    "net/http"
    "github.com/hashicorp/golang-lru"
    "os"
    "encoding/json"
    "sync"
    "errors"
    "strconv"
    "time"
)

// ConfigServiceClient provides API to interact with config service to
// read and watch for configuration changes
type ConfigServiceClient struct {
    httpClient *HttpClient
    dynamicBucketCache *lru.Cache
    staticBucketCache *lru.Cache
    mutex sync.Mutex
}

const Preprod = "in-mumbai-preprod"

var instZoneToCfgsvc map[string]string = map[string]string {
    "in-mumbai-preprod": "http://10.85.42.2",
    "in-mumbai-prod": "",
    "in-chennai-1": "http://10.47.0.101",
}

const LATEST_VERSION = -1

// NewConfigServiceClient creates a new instance of config service client and returns its pointer.
func NewConfigServiceClient(cacheSize int) (*ConfigServiceClient,error) {

    client := &ConfigServiceClient{}
    var url string
    zone := readInstZone()
    url, ok = instZoneToCfgsvc[zone]
    if !ok {
        log.Println("Instance zone not found, defaulting to prepod")
        url = instZoneToCfgsvc[Preprod]
    }

    log.Println("Using URL: " + url)

    httpClient,err := NewHttpClient(&http.Client{Timeout: time.Duration(60 * time.Second)}, url, zone)
    if err != nil {
        return nil, err
    }

    client.dynamicBucketCache, err = lru.NewWithEvict(cacheSize, func(bucketName interface{}, value interface{}) {
        dynamicBucket := value.(*DynamicBucket)
        log.Println("Removing bucket from local cache: ", bucketName)
        dynamicBucket.Disconnected(errors.New("Bucket evicted from cache, please fetch it again"))
        dynamicBucket.shutdown()
    })

    client.staticBucketCache, err = lru.NewWithEvict(cacheSize, func(bucketName interface{}, value interface{}) {
        log.Println("Removing bucket from local cache: ", bucketName)
    })

    if err != nil {
        return nil,err
    } else {
        client.httpClient = httpClient
        return client, nil
    }
}

//Get a dynamic bucket which is auto-updated by a setting watch.
//Keeps a local reference of the static bucket for updating and caching.
func (this *ConfigServiceClient) GetDynamicBucket(name string) (*DynamicBucket, error) {
    if val,ok := this.dynamicBucketCache.Get(name); ok {
        dynamicBucket := val.(*DynamicBucket)
        return dynamicBucket, nil
    } else {
        //Use mutex to ensure the bucket will be fetched only once!
        this.mutex.Lock(); defer this.mutex.Unlock()

        //Check cache again to see if the another thread has
        //already initialized the bucket
        if val,ok := this.dynamicBucketCache.Get(name); ok {
            dynamicBucket := val.(*DynamicBucket)
            return dynamicBucket, nil;
        } else {
            // Initialize the bucket if this the first time
            return this.initDynamicBucket(name)
        }
    }
}

//Initialises a dynamic bucket given the bucket name
func (this *ConfigServiceClient) initDynamicBucket(name string) (*DynamicBucket, error) {
    log.Println("Initializing Config bucket: " + name)

    dynamicBucket := &DynamicBucket{ httpClient: this.httpClient }

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
    if val,ok := this.staticBucketCache.Get(cacheKey(name, version)); ok {
        bucket := val.(*Bucket)
        return bucket, nil
    } else {
        //Use mutex to ensure the bucket will be fetched only once!
        this.mutex.Lock(); defer this.mutex.Unlock()

        //Check cache again to see if the another thread has
        //already initialized the bucket
        if val,ok := this.staticBucketCache.Get(cacheKey(name, version)); ok {
            bucket := val.(*Bucket)
            return bucket, nil;
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
    return name + ":" + strconv.Itoa(version);
}


var instZone struct {
    Zone string `json:"zone"`
}

func readInstZone() string {
    instMetaData, err :=  os.Open("/etc/default/megh/instance_metadata.json")
    if err != nil {
        log.Println("Error opening Instance Metadata json")
    }

    jsonParser := json.NewDecoder(instMetaData)
    if err = jsonParser.Decode(&instZone); err != nil {
        log.Println("parsing config file", err.Error())
    }

    return instZone.Zone
}
