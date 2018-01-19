package cfgsvc
import (
    "testing"
    "github.com/stretchr/testify/assert"
    "net/http"
    "net/url"
    "fmt"
    "net/http/httptest"
    "strings"
    "io/ioutil"
)


var (
    meta BucketMetaData = BucketMetaData{
        bucketMetaData: bucketMetaData{
            Name: "foo",
            Version: 10,
            LastUpdated: 1431597657,
        },
    }
    testBucketData Bucket = Bucket{
       bucket: bucket {
           Meta: &meta,
           Keys: map[string]interface{}{
                "foo":"bar",
                "bar": "baz",
           },
       },
    }
)

func testBucket(t *testing.T, b *Bucket) {
    testMeta(t, b.GetMeta())
    testProperty(t, b.GetKeys())
}

func testMeta(t *testing.T, m *BucketMetaData) {
    assert.Equal(t, m.GetName(), meta.GetName())
    assert.Equal(t, m.GetVersion(), meta.GetVersion())
    assert.Equal(t, m.GetLastUpdated(), meta.GetLastUpdated())
}

func testProperty(t *testing.T, p map[string]interface{}) {
    assert.Equal(t, p, testBucketData.GetKeys())
}

func Test_get_request(t *testing.T) {

    url := ""
    _, client := httpTestTool(200, `{"meta": "hello world"}`, &url)

    resp_body, _ := doRequest(&client, "http://localhost/test123")

    assert.Equal(t, strings.Trim(string(resp_body[:]), "\n"), `{"meta": "hello world"}`)
    assert.Equal(t, url, "http://localhost/test123")
}

func Test_get_vpc_name(t *testing.T) {

    url := ""
    _, client := httpTestTool(200, `{"id":"123456","primary_ip":"10.20.30.40","hostname":"hostname1","vpc_subnet_name":"Fk-Helios","version":2627,"app":"test","zone":"zone1","machine_type":"vm"}`, &url)

    meta := &InstanceMetadata{}
    meta.Id = "123456"
    meta.App = "test"
    meta.Zone = "zone1"
    meta.InstanceGroup = "ig1"
    meta.Hostname = "hostname1"
    meta.PrimaryIP = "10.20.30.40"

    vpcName, err := getVpcSubnetName(&client, meta)

    assert.Nil(t, err)
    assert.Equal(t, "fk-helios", vpcName)
    assert.Equal(t, "http://10.47.255.6:8080/compute/v2/apps/test/zones/zone1/instances/123456", url)
}

func Test_get_properties(t *testing.T) {

    tempFile, err := ioutil.TempFile("", "tempProperties")
    
    assert.Nil(t, err)

    err = ioutil.WriteFile(tempFile.Name(), []byte("host=10.20.30.40\nport=1234"), 0644)
    assert.Nil(t, err)

    props, err := getProperties(tempFile.Name())

    assert.Nil(t, err)
    assert.Equal(t, "10.20.30.40", props["host"])
    assert.Equal(t, "1234", props["port"])
}

func Test_get_overrides_with_port(t *testing.T) {
    tempFile, err := ioutil.TempFile("", "tempProperties")
    
    assert.Nil(t, err)

    err = ioutil.WriteFile(tempFile.Name(), []byte("host=10.20.30.40\nport=1234"), 0644)
    assert.Nil(t, err)

    overrides, err := getOverrides(tempFile.Name())
    assert.Nil(t, err)

    assert.Equal(t, "http://10.20.30.40:1234", overrides.Endpoint)
}

func Test_get_overrides_no_port(t *testing.T) {
    tempFile, err := ioutil.TempFile("", "tempProperties")
    
    assert.Nil(t, err)

    err = ioutil.WriteFile(tempFile.Name(), []byte("host=10.20.30.40\n"), 0644)
    assert.Nil(t, err)

    overrides, err := getOverrides(tempFile.Name())
    assert.Nil(t, err)

    assert.Equal(t, "http://10.20.30.40:80", overrides.Endpoint)
}

func Test_get_overrides_invalid_port(t *testing.T) {
    tempFile, err := ioutil.TempFile("", "tempProperties")
    
    assert.Nil(t, err)

    err = ioutil.WriteFile(tempFile.Name(), []byte("host=10.20.30.40\nport=1234fsdf\n"), 0644)
    assert.Nil(t, err)

    _, err = getOverrides(tempFile.Name())
    assert.NotNil(t, err)
}

func Test_get_overrides_no_file(t *testing.T) {
    _, err := getOverrides("/tmp/sdklfjdnsfkhjkjfnjshbkjdzhkxj")
    assert.NotNil(t, err)
}

func Test_ConmanClient_GetBucket(t *testing.T) {
    url := ""
    server, httpClient := httpTestTool(200, `{"metadata":{"name":"foo","version":10,"lastUpdated":1431597657},"keys":{"foo":"bar","bar":"baz"}}`, &url)
    client, err := NewConfigServiceClient(50)
    client.httpClient, _ = NewHttpClient(&httpClient, server.URL, "in-mumbai-preprod")
    assert.Nil(t, err)
    assert.NotNil(t, client)

    bucket, err := client.GetBucket(meta.GetName(), -1)
    assert.Nil(t, err)
    assert.NotNil(t, bucket)

    testBucket(t, bucket)

}

func httpTestTool(code int, body string, requestServed *string) (*httptest.Server, http.Client) {
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(code)
        w.Header().Set("Content-Type", "application/json")
        fmt.Fprintln(w, body)
    }))
    tr := &http.Transport{
        Proxy: func(req *http.Request) (*url.URL, error) {
            *requestServed = req.URL.String()
            return url.Parse(server.URL)
        },
    }
    client := http.Client{Transport: tr}
    return server, client
}
