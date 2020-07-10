package main

import (
	//"bufio"
	"database/sql"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"net/http"
	"github.com/goware/urlx"
	

	//"github.com/aybabtme/rgbterm"
	"github.com/mailgun/holster"
	_ "github.com/go-sql-driver/mysql"

)

type Monitor struct {
	Domain string `json:"domain"`
	MonitorId int64 `json:"id"` 
	CreatedAt time.Time `json:"created_at"`
}

type MonitorSSL struct{
	monitor_id int64
	tls_check []string
	domain string
	subdomain string 
}
var (
	defaultMaxIdleConnsPerHost = 2000
	defaultTimeout             time.Duration
	defaultKeepAlive           time.Duration = 180 * time.Second
	disableKeepAlives          bool
)
var (
	countSuccess int64
	countErrors  int64
)


var (
	filePath string // location of the file containing the list of URLs to be crawled
	threads  int
	limit    int
	findAll  bool
)

// versionsToCheck are the SSL/TLS protocol versions that will be checked for;
// NOTE: they must be ordered from lower, to higher.
var versionsToCheck = []uint16{
	tls.VersionSSL30,
	tls.VersionTLS10,
	tls.VersionTLS11,
	tls.VersionTLS12,
	tls.VersionTLS13,
}

func NewHTTPClient() *http.Client {
	tr := &http.Transport{
		DisableKeepAlives: disableKeepAlives,

		IdleConnTimeout:     defaultTimeout,
		MaxIdleConnsPerHost: defaultMaxIdleConnsPerHost,
		Proxy:               http.ProxyFromEnvironment,
		Dial: (&net.Dialer{
			Timeout:   defaultTimeout,
			KeepAlive: defaultKeepAlive,
		}).Dial,
		DisableCompression: false,
		TLSClientConfig: &tls.Config{
			// NOTE: TLS cert verification is skipped.
			InsecureSkipVerify: true,
		},
	}

	return &http.Client{
	    CheckRedirect: func(req *http.Request, via []*http.Request) error {
            return http.ErrUseLastResponse
        },
		Timeout:   defaultTimeout,
		Transport: tr,
	}
}
func DateEqual(date1, date2 time.Time) bool {
    y1, m1, d1 := date1.Date()
    y2, m2, d2 := date2.Date()
    return y1 == y2 && m1 == m2 && d1 == d2
}
func versionToString(v uint16) string {
	switch v {
	case tls.VersionSSL30:
		return "3.0"
	case tls.VersionTLS10:
		return "1.0"
	case tls.VersionTLS11:
		return "1.1"
	case tls.VersionTLS12:
		return "1.2"
	case tls.VersionTLS13:
		return "1.3"
	default:
		return  strconv.FormatUint(uint64(v), 16)
	}
}

type sendAlertParam struct{
	MonitorId int64
}

func sendAlert(monitorId int64) (){
		// Put these in the global scope so they don't get converted for each request.
		
		 req, err := http.NewRequest("GET", "send/url/send-mails", nil)
		    if err != nil {
		        //fmt.Println("not ok")
		    }

		    q := req.URL.Query()

		    q.Add("monitorId", strconv.FormatInt(monitorId, 10))
		   
		    req.URL.RawQuery = q.Encode()
		    //fmt.Println(monitorId)
		    client := &http.Client{}
		   	response, err := client.Do(req)
			if err != nil {
			    //fmt.Println("HTTP call failed:", err)
			    return
			}
			//fmt.Println(response.StatusCode)		
			defer response.Body.Close()
   
}
var  parameter = string(os.Args[1])
	
func main() {

	flag.StringVar(&filePath, "f", "urls.txt", "Enter the full path to your text file of URLs.")
	flag.IntVar(&threads, "g", 200, "Number of goroutines.")
	flag.IntVar(&limit, "limit", 0, "Read only the first N entries from file.")
	flag.DurationVar(&defaultTimeout, "timeout", 15*time.Second, "Client timeout.")
	flag.BoolVar(&findAll, "all", false, "Find all supported versions")
	flag.Parse()

	//startTime := time.Now()
	//Trigger finish event once   processing finished
	//defer finish(startTime)
	if(parameter == "all"){
			parameter = "%"	
		}



	runtime.GOMAXPROCS(runtime.NumCPU())
	stdClient := NewHTTPClient()
	_ = stdClient

	db, err :=sql.Open("mysql", "conn")
	if err != nil{
		panic(err.Error())
	}
	db.SetConnMaxLifetime(time.Minute*5);
    db.SetMaxIdleConns(5);
    db.SetMaxOpenConns(5);
	defer db.Close()
	currentDate := int(time.Now().Weekday()) 
	
	
		
	results, err := db.Query("SELECT domain, id FROM `re_monitors` WHERE `monitor_type_id` = 2 AND `id` LIKE ? and `status` = 1 AND `day_of_week`= ?",parameter,currentDate)
	if err != nil{
		panic(err.Error())
	}
	defer results.Close()
	
	// resultsChan is a channel used to send results
	// from the crawler routines to the CSV writer.
	resultsChan := make(chan MonitorSSL, threads*2)

	// resultWriteWaitGroup tracks the write operation for
	// each result; one successful crawl -> one successful write.
	resultWriteWaitGroup := &sync.WaitGroup{}

	// chanCloseCSVExporter is used to close CSVExporter
	chanCloseInsertMonitorSSL := make(chan bool, 0)

	// chanCSVExporterExited is the channel that CSVExporter
	// uses to confirm that it finished all operations.
	chanInsertMonitorSSLExited := make(chan bool, 0)

	go func() {
		// Start CSV exporter goroutine that will receive and save
		// to file the results of the crawler.
		err := InsertMonitorSSL(
			resultsChan,
			resultWriteWaitGroup,
			chanCloseInsertMonitorSSL,
			chanInsertMonitorSSLExited,
		)
		if err != nil {
			// TODO: handle error
			log.Fatalln(err)
		}
	}()

	workers := holster.NewFanOut(threads)

	processor := func(cast interface{}) error {
		// if there are two columns, this is most likely the top-1m.csv file;
		// if the is only one column, this is just a list of URLs.
		row := cast.(Monitor)
		var sslInsert MonitorSSL
		rawHost := row.Domain
		sslInsert.monitor_id = row.MonitorId
		sslInsert.domain = row.Domain
		sslInsert.subdomain = "0"
	
		parsed, err := urlx.ParseWithDefaultScheme(rawHost, "https")
		if err != nil {
			return err
		}

		hostname := parsed.Hostname()

		// add SSL/TLS port if missing:
		if parsed.Port() == "" {
			hostname += ":443"
		} else {
			hostname += ":" + parsed.Port()
		}

		/*errorProcessor := func(err error) error {
			ee := fmt.Errorf("error for %s : %s", hostname, err)
			fmt.Println(ee)
			atomic.AddInt64(&countErrors, 1)
			return ee
		}*/

		task := NewTask(hostname)

		if findAll {

			supportedVersions, errs := task.FindAllWithConcurrencyLimit(len(versionsToCheck), versionsToCheck)
			if errs != nil {
				return nil
				
				} else { 

				// Add results to the row:
				sslInsert.tls_check = append(sslInsert.tls_check,
					strconv.FormatBool(supportedVersions.IsSupported(tls.VersionSSL30)),
					strconv.FormatBool(supportedVersions.IsSupported(tls.VersionTLS10)),
					strconv.FormatBool(supportedVersions.IsSupported(tls.VersionTLS11)),
					strconv.FormatBool(supportedVersions.IsSupported(tls.VersionTLS12)),
					strconv.FormatBool(supportedVersions.IsSupported(tls.VersionTLS13)),
				)
				
				//fmt.Println(fmt.Sprintf("%s supports %v", task.Host, formatVersionArray(supportedVersions)))
			}
			// DEBUG:
			
		} else {
			minVersion, err := task.FindMinSupportedVersion(versionsToCheck)
			if err != nil {
				return nil
				
				//return errorProcessor(fmt.Errorf("error while searching min supported version: %s", err))
			} else {

				/*if minVersion == 0 {
					//return errorProcessor(errors.New("no supported version found"))
					sslInsert.tls_check = "0"
				}*/
				
				sslInsert.tls_check = append(sslInsert.tls_check,
					versionToString(minVersion),
				)
				//add results to array
			}
		}

		// send the updated row to be saved in the CSV file:
		resultWriteWaitGroup.Add(1)
		resultsChan <- sslInsert

		atomic.AddInt64(&countSuccess, 1)

		return nil
	}

	//count := 0
	for results.Next(){

				var monitor Monitor
				err = results.Scan(&monitor.Domain, &monitor.MonitorId)
				if err != nil{
					panic(err.Error())
				}
					workers.Run(
						processor,
						monitor,
					)

			}
	
	// wait for workers to finish:
	errArray := workers.Wait()
	if errArray != nil {
		for _, err := range errArray {
			fmt.Println(err)
		}
	}

	//fmt.Println("waiting for resultWriteWaitGroup")
	// wait for all write operations to be completed:
	resultWriteWaitGroup.Wait()

	//fmt.Println("trying to close chanCloseCSVExporter")
	// send signal to CSVExporter to exit:
	chanCloseInsertMonitorSSL <- true

	//fmt.Println("waiting for chanCSVExporterExited")
	// wait for CSVExporter to confirm exit:
	<-chanInsertMonitorSSLExited
}

type Task struct {
	Host string
}

func NewTask(host string) *Task {
	return &Task{
		Host: host,
	}
}

func (task *Task) FindMinSupportedVersion(versions []uint16) (uint16, error) {

	for _, version := range versions {
		isSupported, err := task.CheckVersion(version)
		if err != nil {
			return 0, err
		}
		if isSupported {
			return version, nil
		}
	}

	return 0, nil
}

//
func (task *Task) FindAllWithConcurrencyLimit(limit int, versions []uint16) (*Array, []error) {
	workers := holster.NewFanOut(limit)

	supportedVersions := NewArray()

	for _, version := range versions {
		workers.Run(
			func(cast interface{}) error {
				version := cast.(uint16)
				isSupported, err := task.CheckVersion(version)
				if err != nil {
					return err
				}
				if isSupported {
					supportedVersions.PushSupportedVersion(version)
				}
				return nil
			},
			version,
		)
	}

	return supportedVersions, workers.Wait()
}

type Array struct {
	values []uint16
	mu     *sync.Mutex
}

func (arr *Array) IsSupported(version uint16) bool {
	return SliceContainsUint16(arr.values, version)
}
func NewArray() *Array {
	return &Array{
		values: make([]uint16, 0),
		mu:     &sync.Mutex{},
	}
}
func (arr *Array) PushSupportedVersion(version uint16) {
	arr.mu.Lock()
	defer arr.mu.Unlock()

	arr.values = append(arr.values, version)
}

func (task *Task) CheckVersion(version uint16) (bool, error) {
	isSupported, err := isVersionSupported(version, task.Host)
	return isSupported, err
}

func SliceContainsUint16(slice []uint16, element uint16) bool {
	for _, elem := range slice {
		if element == elem {
			return true
		}
	}
	return false
}

func isVersionSupported(version uint16, hostPort string) (bool, error) {
	conf := &tls.Config{
		// NOTE:
		InsecureSkipVerify: true,

		MaxVersion: version,
		MinVersion: version,
	}

	conn, err := tls.DialWithDialer(
		&net.Dialer{
			Timeout:   defaultTimeout,
			KeepAlive: -1,
		},
		"tcp",
		hostPort,
		conf,
	)
	if err != nil {
		if isVersionCompatibilityError(err) {
			return false, nil
		}
		// io.EOF usually happens AFTER a successful handshake.
		if err == io.EOF {
			return true, nil
		}
		return false, err
	}
	defer conn.Close()

	return conn.ConnectionState().Version == version, nil
}

func formatVersionArray(versions []uint16) []string {
	var res []string
	for _, v := range versions {
		res = append(res, versionToString(v))
	}
	return res
}

func formatErrorArray(errs []error) string {
	var strArr []string

	for _, err := range errs {
		strArr = append(strArr, fmt.Sprintf("%q", err.Error()))
	}

	return fmt.Sprintf("[%s]", strings.Join(strArr, ", "))
}

// isVersionCompatibilityError tells whether the error is about the protocol version,
// (or most likely about it, as in the "handshake failure" case).
func isVersionCompatibilityError(err error) bool {
	return strings.Contains(err.Error(), "no supported versions satisfy MinVersion and MaxVersion") ||
		strings.Contains(err.Error(), "server selected unsupported protocol version") ||
		strings.Contains(err.Error(), "protocol version not supported") ||
		strings.Contains(err.Error(), "handshake failure") ||
		strings.Contains(err.Error(), "remote error: tls: internal error") ||
		strings.Contains(err.Error(), "read: connection reset by peer") ||
		strings.Contains(err.Error(), "wsarecv: An existing connection was forcibly closed by the remote host")
}




func InsertMonitorSSL(
	resultsChan chan MonitorSSL,
	resultWriteWaitGroup *sync.WaitGroup,
	doClose chan bool,
	closed chan bool,
) error {
	db, err :=sql.Open("mysql", "conn")
		if err != nil{
			panic(err.Error())
		}
		db.SetConnMaxLifetime(time.Minute*5);
        db.SetMaxIdleConns(5);
        db.SetMaxOpenConns(5);
		defer db.Close()
	for {
		select {
		case <-doClose:
			// signal that CSVExporter has exited:
			closed <- true
			return nil
		case resultRow := <-resultsChan:
			{
				fmt.Println(resultRow)

				justString := strings.Join(resultRow.tls_check," ")
				mostRecentTlsVersion, err := strconv.ParseFloat(justString, 32)
				if err == nil {

    				mostRecentTlsVersion := strconv.FormatFloat(mostRecentTlsVersion, 'f', 1, 32)
					
    				var currentSecureErrors int
    				secure_errors  := db.QueryRow("SELECT secure_errors FROM `re_monitor_ssl_https` WHERE `monitor_id` = ? AND `subdomain` = 0 ORDER BY id ASC limit 1", resultRow.monitor_id)
    				secure_errors.Scan(&currentSecureErrors)

    				var currentTlsVersion sql.NullString
    				tls_version := db.QueryRow("SELECT  tls_version FROM `re_monitor_ssl_https` WHERE `monitor_id` = ? AND `subdomain` = 0 ORDER BY id ASC limit 1", resultRow.monitor_id)
    				tls_version.Scan(&currentTlsVersion)
    				//currentTlsVersion2 := strconv.FormatFloat(currentTlsVersion, 'f', 2, 32)
    				if(currentTlsVersion.Valid == false  ){
    					if(mostRecentTlsVersion > strconv.FormatFloat(1.1, 'f', 1, 32)){
    					db.Exec("UPDATE `re_monitor_ssl_https` SET `tls_version`= ?  where `monitor_id`=?  AND `subdomain`= ?",mostRecentTlsVersion, resultRow.monitor_id , "0")
	    				}else{
	    					db.Exec("UPDATE `re_monitor_ssl_https` SET `tls_version`= ? ,`secure_errors`= `secure_errors`+1 where `monitor_id`=?  AND `subdomain`= ?",mostRecentTlsVersion, resultRow.monitor_id , "0")
	    				}

    				}else if(currentTlsVersion.String != mostRecentTlsVersion){
    				 
						if(currentTlsVersion.String < strconv.FormatFloat(1.2, 'f', 1, 32)){
							
							if(mostRecentTlsVersion > strconv.FormatFloat(1.1, 'f', 1, 32)){
								
								db.Exec("UPDATE `re_monitor_ssl_https` SET `tls_version`= ? ,`secure_errors`= `secure_errors`-1 where `monitor_id`=?  AND `subdomain`= ?",mostRecentTlsVersion, resultRow.monitor_id , "0")
							}
	
						}else{
							
							if(mostRecentTlsVersion < strconv.FormatFloat(1.2, 'f', 1, 32)){
								//do dencrement on secure_errors
								db.Exec("UPDATE `re_monitor_ssl_https` SET `tls_version`= ? ,`secure_errors`= `secure_errors`+1 where `monitor_id`=?  AND `subdomain`= ?",mostRecentTlsVersion, resultRow.monitor_id , "0")
							}

						}

					}

					if(parameter != "%"){
						sendAlert(resultRow.monitor_id)
					}
				}
			}
			
				
				resultWriteWaitGroup.Done()
			}
		}
	}


//Run final processing
func finish(startTime time.Time) {
	totalTime := time.Now().Sub(startTime).Truncate(time.Second)
	totalSeconds := totalTime / time.Second

	// avoid division by zero:
	if totalSeconds == 0 {
		totalSeconds = 1
	}

	fmt.Println("Processing complete")
	fmt.Printf("Total time: %d seconds \n", totalSeconds)
	fmt.Printf("Total successful loads: %d \n", countSuccess)
	fmt.Printf("Total errors: %d \n", countErrors)
	fmt.Printf(
		"-f=%s -g=%v -limit=%v -timeout=%s | success=%v, errors=%v, success/s=%d/s, errors/s=%d/s %s (=%vs)\n",
		filePath,
		threads,
		limit,
		defaultTimeout,
		countSuccess,
		countErrors,
		countSuccess/int64(totalSeconds),
		countErrors/int64(totalSeconds),
		totalTime,
		int(totalSeconds),
	)
}

