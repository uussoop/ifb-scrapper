package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antchfx/htmlquery"
	"github.com/gin-gonic/gin"
	ptime "github.com/yaa110/go-persian-calendar"
	"golang.org/x/net/html"
	"gorm.io/gorm"
)

var token = ""

type NegotiateResponse struct {
	Url                string  `json:"Url"`
	ConnectionToken    string  `json:"ConnectionToken"`
	ConnectionId       string  `json:"ConnectionId"`
	KeepAliveTimeout   float64 `json:"KeepAliveTimeout"`
	DisconnectTimeout  float64 `json:"DisconnectTimeout"`
	TryWebSockets      bool    `json:"TryWebSockets"`
	WebSocketServerUrl string  `json:"WebSocketServerUrl"`
	ProtocolVersion    string  `json:"ProtocolVersion"`
}

type StreamMessage struct {
	C string        `json:"C"`
	M []interface{} `json:"M"`
}
type AppCache struct {
	mu    sync.RWMutex
	Table map[string]interface{}
}

func NewAppCache() *AppCache {
	return &AppCache{
		Table: make(map[string]interface{}),
	}
}

func (c *AppCache) UpdateSingleTable(data interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Table = data.(map[string]interface{})
}

func (c *AppCache) UpdateRow(data interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rowDataSlice, ok := data.([]interface{})
	if !ok {
		fmt.Println("Error: UpdateRow received unexpected data type")
		return
	}
	for _, row := range rowDataSlice {
		rowData, ok := row.(map[string]interface{})
		if !ok {
			fmt.Println("Error: Row data is not in expected format")
			continue
		}
		symbolId, ok := rowData["SymbolId"].(float64)
		if !ok {
			fmt.Println("Error: SymbolId is not a float64")
			continue
		}
		c.Table[fmt.Sprintf("%.0f", symbolId)] = rowData
	}
}

func (c *AppCache) GetTable() map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Table
}
func (c *AppCache) GetFilteredTable(marketNumber int) map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()
	filteredTable := make(map[string]interface{})
	for key, value := range c.Table {
		if row, ok := value.(map[string]interface{}); ok {
			if rowMarketNumber, ok := row["MarketNumber"].(float64); ok {
				if int(rowMarketNumber) == marketNumber {
					filteredTable[key] = value
				}
			}
		}
	}
	return filteredTable
}

var appCache = NewAppCache()

func main() {
	if token = os.Getenv("stock_token"); token == "" {
		panic("no stock_token in env")
	}
	go startStreamProcessing()

	r := gin.Default()
	r.Use(AuthMiddleware())
	r.GET("/table", getTable)
	r.GET("/table/filter", getFilteredTable)
	r.GET("/bond", getBondDetail)
	r.Run(":8080")
}
func getBondDetail(c *gin.Context) {
	symbolidStr := c.Query("id")
	detailURL := fmt.Sprintf("https://www.ifb.ir/Instrumentsmfi.aspx?id=%s", symbolidStr)
	bond, err := getBondDetails(&http.Client{}, detailURL)
	if err != nil {
		c.AbortWithError(500, err)
	}
	c.JSON(200, bond)
}
func getTable(c *gin.Context) {
	c.JSON(200, appCache.GetTable())
}

func startStreamProcessing() {
	backoff := time.Second

	for {
		err := processStream()
		if err != nil {
			fmt.Printf("Error processing stream: %v\n", err)
			fmt.Printf("Restarting in %v\n", backoff)
			time.Sleep(backoff)
			backoff *= 2
			if backoff > time.Minute*5 {
				backoff = time.Minute * 5
			}
		} else {
			backoff = time.Second
		}
	}
}

func getInitialCookies(url string) (string, error) {
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var cookieStrings []string
	for _, cookie := range resp.Cookies() {
		cookieStrings = append(cookieStrings, cookie.Name+"="+cookie.Value)
	}

	return strings.Join(cookieStrings, "; "), nil
}

func negotiate(url string, connectionData string, timestamp int64, cookies string) (*NegotiateResponse, error) {
	client := &http.Client{}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	q := req.URL.Query()
	q.Add("connectionData", connectionData)
	q.Add("_", fmt.Sprint(timestamp))
	req.URL.RawQuery = q.Encode()

	req.Header.Set("Accept", "text/plain, */*; q=0.01")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	req.Header.Set("Referer", "https://www.ifb.ir/StockQoute.aspx")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Cookie", cookies)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var negotiateResp NegotiateResponse
	err = json.Unmarshal(body, &negotiateResp)
	if err != nil {
		return nil, err
	}

	return &negotiateResp, nil
}

func sendPostRequest(requrl string, connectionID string, cookies string) error {
	client := &http.Client{}

	data := map[string]interface{}{
		"H": "myhub",
		"M": "letsStart",
		"A": []string{connectionID},
		"I": 0,
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return err
	}

	formData := url.Values{}
	formData.Set("data", string(jsonData))

	req, err := http.NewRequest("POST", requrl, strings.NewReader(formData.Encode()))
	if err != nil {
		return err
	}

	req.Header.Set("Accept", "text/plain, */*; q=0.01")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("Origin", "https://www.ifb.ir")
	req.Header.Set("Referer", "https://www.ifb.ir/StockQoute.aspx")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36")
	req.Header.Set("Cookie", cookies)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	fmt.Printf("POST response: %s\n", string(body))
	return nil
}

func processStream() error {
	cookies, err := getInitialCookies("https://www.ifb.ir/StockQoute.aspx")
	if err != nil {
		return fmt.Errorf("error getting initial cookies: %v", err)
	}

	negotiateResp, err := negotiate("https://www.ifb.ir/signalr/negotiate", "[{\"name\":\"myhub\"}]", time.Now().UnixNano()/int64(time.Millisecond), cookies)
	if err != nil {
		return fmt.Errorf("error negotiating: %v", err)
	}

	connectURL := fmt.Sprintf("https://www.ifb.ir/signalr/connect?transport=serverSentEvents&connectionToken=%s&connectionData=[{\"name\":\"myhub\"}]&tid=%d",
		url.QueryEscape(negotiateResp.ConnectionToken),
		rand.Intn(10))

	client := &http.Client{}
	req, err := http.NewRequest("GET", connectURL, nil)
	if err != nil {
		return fmt.Errorf("error creating request: %v", err)
	}

	headers := map[string]string{
		"Accept":             "text/event-stream",
		"Accept-Encoding":    "gzip, deflate, br, zstd",
		"Accept-Language":    "en-US,en;q=0.9",
		"Cache-Control":      "no-cache",
		"Connection":         "keep-alive",
		"Host":               "www.ifb.ir",
		"Referer":            "https://www.ifb.ir/StockQoute.aspx",
		"Sec-CH-UA":          "\"Google Chrome\";v=\"129\", \"Not=A?Brand\";v=\"8\", \"Chromium\";v=\"129\"",
		"Sec-CH-UA-Mobile":   "?0",
		"Sec-CH-UA-Platform": "\"macOS\"",
		"Sec-Fetch-Dest":     "empty",
		"Sec-Fetch-Mode":     "cors",
		"Sec-Fetch-Site":     "same-origin",
		"User-Agent":         "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36",
		"Cookie":             cookies,
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("error sending request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	reader := bufio.NewReader(resp.Body)

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return fmt.Errorf("stream closed")
			}
			return fmt.Errorf("error reading line: %v", err)
		}

		if strings.HasPrefix(line, "data:") {
			data := strings.TrimPrefix(line, "data: ")
			data = strings.TrimSpace(data)

			if data == "initialized" {
				sendURL := fmt.Sprintf("https://www.ifb.ir/signalr/send?transport=serverSentEvents&connectionToken=%s&connectionData=[{\"name\":\"myhub\"}]",
					url.QueryEscape(negotiateResp.ConnectionToken))

				err = sendPostRequest(sendURL, negotiateResp.ConnectionId, cookies)
				if err != nil {
					return fmt.Errorf("error sending POST request: %v", err)
				}
			} else {
				var message StreamMessage
				err := json.Unmarshal([]byte(data), &message)
				if err != nil {
					fmt.Printf("Error unmarshaling message: %v\n", err)
					continue
				}

				processStreamMessage(message)
			}
		}
	}
}

func processStreamMessage(message StreamMessage) {
	for _, m := range message.M {
		msg := m.(map[string]interface{})
		switch msg["M"] {
		case "updateSingleTable":
			data := msg["A"].([]interface{})[0]
			appCache.UpdateSingleTable(convertToSymbolIdMap(data))
		case "updateRow":
			data := msg["A"].([]interface{})[0]
			appCache.UpdateRow(data)
		}
	}
}

func convertToSymbolIdMap(data interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	dataSlice := data.([]interface{})
	for _, item := range dataSlice {
		if itemMap, ok := item.(map[string]interface{}); ok {
			if symbolId, exists := itemMap["SymbolId"]; exists {
				result[fmt.Sprintf("%.0f", symbolId.(float64))] = itemMap
			}
		}
	}
	return result
}

func getFilteredTable(c *gin.Context) {
	marketNumberStr := c.Query("market")
	marketNumber, err := strconv.Atoi(marketNumberStr)
	if err != nil {
		c.JSON(400, gin.H{"error": "Invalid market number"})
		return
	}
	filteredTable := appCache.GetFilteredTable(marketNumber)
	c.JSON(200, filteredTable)
}

func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authorization header is required"})
			c.Abort()
			return
		}

		bearerToken := strings.Split(authHeader, " ")
		if len(bearerToken) != 2 || bearerToken[0] != "Bearer" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid authorization header format"})
			c.Abort()
			return
		}

		tokenString := bearerToken[1]

		if tokenString != token {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
			c.Abort()
			return
		}

		c.Next()
	}
}

type Bond struct {
	gorm.Model
	SymbolId            string
	Symbol              string
	SubClass            string
	LastTradingDate     time.Time
	PublicationDate     time.Time
	MaturityDate        time.Time
	LastPrice           float64
	FaceValue           float64
	NominalInterestRate float64
	Volume              float64
	YTM                 float64
	CalculatedYTM       float64
	Interval            int
}

func getBondDetails(client *http.Client, bondURL string) (Bond, error) {
	var bond Bond
	parsedURL, err := url.Parse(bondURL)
	if err != nil {
		fmt.Println("Error parsing URL:", err)
	}

	// Get the query parameters
	queryParams := parsedURL.Query()

	// Extract the 'id' parameter
	bond.SymbolId = queryParams.Get("id")
	println(bondURL)
	resp, err := client.Get(bondURL)
	if err != nil {
		return bond, err
	}
	defer resp.Body.Close()

	doc, err := htmlquery.Parse(resp.Body)
	if err != nil {
		return bond, err
	}

	bond.Symbol = safeGetText(doc, "/html/body/div/div[3]/div[1]/div[1]/div[1]/form/div[2]/div/div[3]/div[1]/div[1]/table/tbody/tr[2]/td[2]")
	bond.SubClass = safeGetText(doc, "/html/body/div/div[3]/div[1]/div[1]/div[1]/form/div[2]/div/div[3]/div[1]/div[1]/table/tbody/tr[2]/td[2]")
	fmt.Println(bond.Symbol)
	fmt.Println(bond.SubClass)

	lastPrice := safeGetText(doc, "/html/body/div/div[3]/div[1]/div[1]/div[1]/form/div[1]/div[2]/div[1]/span[2]")
	bond.LastPrice, _ = removeCommasAndConvertToFloat(persianToEnglishNumber(lastPrice))
	fmt.Println(lastPrice)

	maturityDateStr := safeGetText(doc, "/html/body/div/div[3]/div[1]/div[1]/div[1]/form/div[2]/div/div[3]/div[2]/div[2]/div[2]/table/tbody/tr[5]/td[2]")
	bond.MaturityDate, _ = parseDate(maturityDateStr)
	fmt.Println(maturityDateStr)

	faceValue := safeGetText(doc, "/html/body/div/div[3]/div[1]/div[1]/div[1]/form/div[2]/div/div[3]/div[2]/div[2]/div[1]/table/tbody/tr[2]/td[2]")
	bond.FaceValue, _ = removeCommasAndConvertToFloat(persianToEnglishNumber(faceValue))
	fmt.Println(faceValue)
	publicationDateStr := safeGetText(doc, "/html/body/div/div[3]/div[1]/div[1]/div[1]/form/div[2]/div/div[3]/div[2]/div[2]/div[1]/table/tbody/tr[5]/td[2]")
	bond.PublicationDate, _ = parseDate(publicationDateStr)
	fmt.Println(publicationDateStr)

	nominalInterestRate := safeGetText(doc, "/html/body/div/div[3]/div[1]/div[1]/div[1]/form/div[2]/div/div[3]/div[2]/div[2]/div[2]/table/tbody/tr[2]/td[2]")
	bond.NominalInterestRate, _ = removeCommasAndConvertToFloat(persianToEnglishNumber(nominalInterestRate))
	fmt.Println(nominalInterestRate)

	intervalStr := safeGetText(doc, "/html/body/div/div[3]/div[1]/div[1]/div[1]/form/div[2]/div/div[3]/div[2]/div[2]/div[2]/table/tbody/tr[7]/td[2]")
	intervalStr = strings.TrimSpace(strings.ReplaceAll(intervalStr, "ماه", ""))
	bond.Interval, _ = strconv.Atoi(persianToEnglishNumber(intervalStr))
	fmt.Println(intervalStr)

	return bond, nil
}

func removeCommasAndConvertToFloat(numberStr string) (float64, error) {
	cleanStr := strings.ReplaceAll(numberStr, ",", "")
	return strconv.ParseFloat(cleanStr, 64)
}

func parseDate(dateStr string) (time.Time, error) {
	// Assuming the date format is "YYYY/MM/DD"
	parts := strings.Split(dateStr, "/")
	if len(parts) != 3 {
		return time.Time{}, fmt.Errorf("invalid date format: %s", dateStr)
	}
	year, err := strconv.Atoi(parts[0])
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid year: %s", parts[0])
	}
	month, err := strconv.Atoi(parts[1])
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid month: %s", parts[1])
	}
	day, err := strconv.Atoi(parts[2])
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid day: %s", parts[2])
	}

	// Convert Jalali to Gregorian
	persianDate := ptime.Date(year, ptime.Month(month), day, 0, 0, 0, 0, time.UTC)
	gregorianDate := persianDate.Time()

	return gregorianDate, nil
}

func safeGetText(doc *html.Node, xpath string) string {
	nodes, err := htmlquery.QueryAll(doc, xpath)
	if err != nil {
		fmt.Printf("Error querying XPath '%s': %v\n", xpath, err)
		return ""
	}
	if len(nodes) == 0 {
		fmt.Printf("No nodes found for XPath '%s'\n", xpath)
		return ""
	}
	return strings.TrimSpace(htmlquery.InnerText(nodes[0]))
}

var persianNumbers = map[string]string{
	"۰": "0", "۱": "1", "۲": "2", "۳": "3", "۴": "4",
	"۵": "5", "۶": "6", "۷": "7", "۸": "8", "۹": "9",
}

func persianToEnglishNumber(persianStr string) string {
	for persian, english := range persianNumbers {
		persianStr = strings.ReplaceAll(persianStr, persian, english)
	}
	return persianStr
}
