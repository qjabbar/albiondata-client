package client

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/ao-data/albiondata-client/lib"
	"github.com/ao-data/albiondata-client/log"
	uuid "github.com/nu7hatch/gouuid"
)

type operationAuctionGetOffers struct {
	Category         string   `mapstructure:"1"`
	SubCategory      string   `mapstructure:"2"`
	Quality          string   `mapstructure:"5"`
	Enchantment      uint32   `mapstructure:"6"`
	EnchantmentLevel string   `mapstructure:"10"`
	ItemIds          []uint16 `mapstructure:"8"`
	MaxResults       uint32   `mapstructure:"12"`
	IsAscendingOrder bool     `mapstructure:"14"`
}

func (op operationAuctionGetOffers) Process(state *albionState) {
	log.Debug("Got AuctionGetOffers operation...")
	state.WaitingForMarketData = true
}

type operationAuctionGetOffersResponse struct {
	MarketOrders []string `mapstructure:"0"`
}

func (op operationAuctionGetOffersResponse) Process(state *albionState) {
	log.Debug("Got response to AuctionGetOffers operation...")
	state.WaitingForMarketData = false

	if !state.IsValidLocation() {
		return
	}

	var orders []*lib.MarketOrder

	for _, v := range op.MarketOrders {
		// Unmarshal market order data to map
		var marketOrder map[string]interface{}
		err2 := json.Unmarshal([]byte(v), &marketOrder)
		if err2 != nil {
			log.Fatal(err2)
		}

		// Pull the location
		location, _ := marketOrder["LocationId"].(string)

		// if the location has @, it is either a rest or smugglers den
		if strings.Contains(location, "@") {

			// Set the location in the market order
			marketOrder["LocationId"] = location

			// Marshal the map back to json
			newJson, _ := json.Marshal(marketOrder)

			// Set the new json back to the market order
			v = string(newJson)
		}

		order := &lib.MarketOrder{}

		err := json.Unmarshal([]byte(v), order)
		if err != nil {
			log.Errorf("Problem converting market order to internal struct: %v", err)
		}

		// Set the location only if its string(nil). Smugglers Dens pull locations directly from the market data (above)
		// while the orignal cities have a null location ID and is pulled from the client state.
		if order.LocationID == "" {
			order.LocationID = state.LocationId
		}

		orders = append(orders, order)
	}

	if len(orders) < 1 {
		return
	}

	upload := lib.MarketUpload{
		Orders: orders,
	}

	identifier, _ := uuid.NewV4()
	log.Infof("Sending %d live market sell orders to ingest (Identifier: %s)", len(orders), identifier)
	
	// Send to public uploaders (original behavior)
	sendMsgToPublicUploaders(upload, lib.NatsMarketOrdersIngest, state, identifier.String())
	
	// Send to private HTTP endpoints (NEW!)
	op.sendToPrivateHTTP(upload, state, identifier.String())
}

// NEW FUNCTION: Send to private HTTP endpoint
func (op operationAuctionGetOffersResponse) sendToPrivateHTTP(upload lib.MarketUpload, state *albionState, identifier string) {
	if len(state.PrivateHTTPURLs) == 0 {
		return
	}

	for _, baseURL := range state.PrivateHTTPURLs {
		// Ensure URL ends with /ingest
		url := baseURL
		if !strings.HasSuffix(url, "/ingest") {
			url = strings.TrimSuffix(url, "/") + "/ingest"
		}

		// Marshal upload to JSON
		jsonData, err := json.Marshal(upload)
		if err != nil {
			log.Errorf("Failed to marshal data for private HTTP: %v", err)
			continue
		}

		// Create HTTP client with timeout
		client := &http.Client{
			Timeout: 10 * time.Second,
		}

		// Send POST request
		resp, err := client.Post(url, "application/json", bytes.NewBuffer(jsonData))
		if err != nil {
			log.Errorf("Failed to send to private HTTP endpoint %s: %v", url, err)
			continue
		}
		defer resp.Body.Close()

		// Check response
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			log.Infof("✅ Successfully sent %d orders to private endpoint: %s (Identifier: %s)", len(upload.Orders), url, identifier)
		} else {
			log.Errorf("Private HTTP endpoint returned status %d for %s", resp.StatusCode, url)
		}
	}
}
