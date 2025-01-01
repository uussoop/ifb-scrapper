# IFB Scraper Documentation

## 1. System Overview
The IFB Scraper is a Go application designed to:
- Stream real-time market data from the Iran Fara Bourse (IFB) website
- Provide REST APIs for accessing market data
- Handle bond detail scraping and processing
- Maintain a thread-safe cache of market data

## 2. Core Components


### 2.1 Data Streaming
The application uses SignalR for real-time data streaming:
- Establishes connection through a multi-step process:
  1. Gets initial cookies
  2. Negotiates connection
  3. Establishes Server-Sent Events (SSE) connection
  4. Processes incoming data stream
- Implements automatic reconnection with exponential backoff
- Handles different message types: `updateSingleTable` and `updateRow`


### 2.2 Data Caching
Thread-safe caching mechanism:
- Uses `AppCache` struct with RWMutex for concurrent access
- Supports both full table updates and individual row updates
- Provides filtered data access based on market number
- Methods:
  - `UpdateSingleTable`: Updates entire cache
  - `UpdateRow`: Updates specific rows
  - `GetTable`: Retrieves full cache
  - `GetFilteredTable`: Retrieves market-specific data

### 2.3 API Endpoints
REST API built with Gin framework:
- `/table` - Get full market data
- `/table/filter` - Get filtered market data by market number
- `/bond` - Get bond details
- All endpoints require token authentication

## 3. Security Considerations

### 3.1 Authentication
- Token-based authentication implemented via middleware
- Token must be provided in environment variable `stock_token`
- All API requests require Bearer token in Authorization header
- Requests without valid token receive 401 Unauthorized response

### 3.2 Error Handling
- Implements exponential backoff for connection retries
- Maximum backoff time of 5 minutes
- Graceful handling of stream disconnections
- Proper error responses for API endpoints

## 4. Data Processing

### 4.1 Bond Details
Bond scraping includes:
- Symbol information
- Price data
- Dates (maturity, publication, last trading)
- Financial details (face value, interest rates)
- Volume and YTM calculations

### 4.2 Date Handling
Special handling for Persian (Jalali) calendar:
- Converts Persian dates to Gregorian
- Uses `go-persian-calendar` package
- Handles date parsing and validation

### 4.3 Number Processing
- Handles Persian number conversion
- Removes commas from numeric strings
- Converts string numbers to float64

## 5. Important Dependencies
Required external packages:
- `github.com/gin-gonic/gin` - Web framework
- `github.com/antchfx/htmlquery` - HTML parsing
- `github.com/yaa110/go-persian-calendar` - Persian calendar support
- `gorm.io/gorm` - ORM (though currently minimal usage)

## 6. Configuration Requirements

### 6.1 Environment Variables
- `stock_token` - Required for authentication

### 6.2 Network Requirements
- Requires access to www.ifb.ir
- Handles HTTPS connections
- Uses specific user agent strings

## 7. Potential Improvement Areas
1. Database integration (GORM structure exists but isn't fully utilized)
2. More comprehensive error logging
3. Metrics collection
4. Rate limiting
5. Request timeouts
6. Input validation
7. Response caching
8. API documentation

## 8. Common Issues to Watch

### 8.1 Connection Handling
- Monitor for connection drops
- Watch for changes in IFB website structure
- Check for SignalR protocol changes

### 8.2 Data Processing
- Validate Persian to English number conversion
- Verify date conversion accuracy
- Monitor memory usage in cache

### 8.3 Performance
- Watch for memory leaks in long-running streams
- Monitor goroutine count
- Check response times under load

## 9. Testing
Currently no explicit testing code, consider adding:
- Unit tests for data processing
- Integration tests for API endpoints
- Load tests for streaming functionality
- Mock tests for external dependencies

## 10. Deployment Notes
The application:
- Runs on port 8080
- Requires HTTPS capability
- Needs persistent network connection
- Should handle reconnection automatically