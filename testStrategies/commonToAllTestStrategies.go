package testStrategies

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"github.com/xtracdev/automated-perf-test/perfTestUtils"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	SERVICE_BASED_TESTING = "ServiceBased"
	SUITE_BASED_TESTING   = "SuiteBased"
)

var globals map[string]string

func init() {
	//Initilize globals map
	globals = make(map[string]string)
}

type Header struct {
	Value string `xml:",chardata"`
	Key   string `xml:"key,attr"`
}

//This struct defines test definition
type TestDefinition struct {
	XMLName            xml.Name             `xml:"testDefinition"`
	TestName           string               `xml:"testName"`
	HttpMethod         string               `xml:"httpMethod"`
	BaseUri            string               `xml:"baseUri"`
	Multipart          bool                 `xml:"multipart"`
	Payload            string               `xml:"payload"`
	MultipartPayload   []multipartFormField `xml:"multipartPayload>multipartFormField"`
	ResponseStatusCode int                  `xml:"responseStatusCode"`
	Headers            []Header             `xml:"headers>header"`
	ResponseProperties []string             `xml:"responseProperties>value"`
}

//This struct defines a load test scenario
type TestSuiteDefinition struct {
	XMLName      xml.Name `xml:"testSuite"`
	Name         string   `xml:"name"`
	TestStrategy string   `xml:"testStrategy"`
	TestCases    []string `xml:"testCases>testCase"`
}

//This struct defines a load test scenario
type TestSuite struct {
	XMLName      xml.Name          `xml:"testSuite"`
	Name         string            `xml:"name"`
	TestStrategy string            `xml:"testStrategy"`
	TestCases    []*TestDefinition `xml:"testCases>testCase"`
}

type multipartFormField struct {
	FieldName   string `xml:"fieldName"`
	FieldValue  string `xml:"fieldValue"`
	FileName    string `xml:"fileName"`
	FileContent []byte `xml:"fileContent"`
}

func (ts *TestSuite) BuildTestSuite(configurationSettings *perfTestUtils.Config) {
	fmt.Println("Building Test Suite ....")

	if configurationSettings.TestSuite == "" {
		ts.Name = "Default"
		ts.TestStrategy = SERVICE_BASED_TESTING

		//If no test suite has been defined, treat and all test case files as the suite
		d, err := os.Open(configurationSettings.TestCaseDir)
		if err != nil {
			fmt.Println("Failed to open test definitions directory. Error:", err)
			os.Exit(1)
		}
		defer d.Close()

		fi, err := d.Readdir(-1)
		if err != nil {
			fmt.Println("Failed to read files in test definitions directory. Error:", err)
			os.Exit(1)
		}
		if len(fi) == 0 {
			fmt.Println("No test case files found in specified directory ", configurationSettings.TestCaseDir)
			os.Exit(1)
		}

		for _, fi := range fi {
			bs, err := ioutil.ReadFile(configurationSettings.TestCaseDir + "/" + fi.Name())
			if err != nil {
				fmt.Println("Failed to read test file. Filename: ", fi.Name(), err)
				continue
			}

			testDefinition := new(TestDefinition)
			xml.Unmarshal(bs, &testDefinition)
			ts.TestCases = append(ts.TestCases, testDefinition)
		}
	} else {
		//If a test suite has been defined, load in all tests associated with the test suite.
		bs, err := ioutil.ReadFile(configurationSettings.TestSuiteDir + "/" + configurationSettings.TestSuite)
		if err != nil {
			fmt.Println("Failed to read test suite defination file. Filename: ", configurationSettings.TestSuiteDir+"/"+configurationSettings.TestSuite, " ", err)
			os.Exit(1)
		}
		testSuiteDefinition := new(TestSuiteDefinition)
		xml.Unmarshal(bs, &testSuiteDefinition)

		ts.Name = testSuiteDefinition.Name
		ts.TestStrategy = testSuiteDefinition.TestStrategy
		for _, fi := range testSuiteDefinition.TestCases {
			bs, err := ioutil.ReadFile(configurationSettings.TestCaseDir + "/" + fi)
			if err != nil {
				fmt.Println("Failed to read test file. Filename: ", fi, err)
				continue
			}

			testDefinition := new(TestDefinition)
			xml.Unmarshal(bs, &testDefinition)
			ts.TestCases = append(ts.TestCases, testDefinition)
		}

	}
}

func (testDefinition *TestDefinition) BuildAndSendRequest(targetHost string, targetPort string) int64 {

	var req *http.Request

	if !testDefinition.Multipart {
		if testDefinition.Payload != "" {
			paylaod := testDefinition.Payload
			newPayload := substituteRequestValues(&paylaod)
			req, _ = http.NewRequest(testDefinition.HttpMethod, "http://"+targetHost+":"+targetPort+testDefinition.BaseUri, strings.NewReader(newPayload))
		} else {
			req, _ = http.NewRequest(testDefinition.HttpMethod, "http://"+targetHost+":"+targetPort+testDefinition.BaseUri, nil)
		}
	} else {
		if testDefinition.HttpMethod != "POST" {
			//log.Fatal("Multipart request has to be 'POST' method.")
			fmt.Println("Multipart request has to be 'POST' method.")
		} else {
			body := new(bytes.Buffer)
			writer := multipart.NewWriter(body)
			for _, field := range testDefinition.MultipartPayload {
				if field.FileName == "" {
					writer.WriteField(field.FieldName, field.FieldValue)
				} else {
					part, _ := writer.CreateFormFile(field.FieldName, field.FileName)
					io.Copy(part, bytes.NewReader(field.FileContent))
				}
			}
			writer.Close()
			req, _ = http.NewRequest(testDefinition.HttpMethod, "http://"+targetHost+":"+targetPort+testDefinition.BaseUri, body)
			req.Header.Set("Content-Type", writer.FormDataContentType())
		}
	}

	//add headers
	for _, v := range testDefinition.Headers {
		req.Header.Add(v.Key, v.Value)
	}
	startTime := time.Now()
	if resp, err := (&http.Client{}).Do(req); err != nil {
		//log.Error("Error by firing request: ", req, "Error:", err)
		fmt.Println("Error by firing request: ", req, "Error:", err)
		return 0
	} else {

		timeTaken := time.Since(startTime)

		body, _ := ioutil.ReadAll(resp.Body)
		defer resp.Body.Close()

		//Validate service response
		contentLengthOk := perfTestUtils.ValidateResponseBody(body, testDefinition.TestName)
		responseCodeOk := perfTestUtils.ValidateResponseStatusCode(resp.StatusCode, testDefinition.ResponseStatusCode, testDefinition.TestName)
		responseTimeOK := perfTestUtils.ValidateServiceResponseTime(timeTaken.Nanoseconds(), testDefinition.TestName)

		if contentLengthOk && responseCodeOk && responseTimeOK {
			extracResponseValues(testDefinition.TestName, body, testDefinition.ResponseProperties)
			return timeTaken.Nanoseconds()
		} else {
			return 0
		}
	}
}

func substituteRequestValues(requestBody *string) string {

	requestPayloadCopy := *requestBody

	r := regexp.MustCompile("{{(.+)?}}")
	res := r.FindAllString(*requestBody, -1)

	if len(res) > 0 {
		for _, property := range res {
			//remove placeholder syntax
			cleanedPropertyName := strings.TrimPrefix(property, "{{")
			cleanedPropertyName = strings.TrimSuffix(cleanedPropertyName, "}}")
			//lookup value in the globals map
			value := globals[cleanedPropertyName]
			if value != "" {
				requestPayloadCopy = strings.Replace(requestPayloadCopy, property, value, 1)
			}
		}

	}
	return requestPayloadCopy
}

func extracResponseValues(testCaseName string, body []byte, resposneProperties []string) {
	for _, name := range resposneProperties {
		if globals[testCaseName+"."+name] == "" {
			r := regexp.MustCompile("<(.+)?:" + name + ">(.+)?</(.+)?:" + name + ">")
			res := r.FindStringSubmatch(string(body))
			globals[testCaseName+"."+name] = res[2]
		}
	}
}