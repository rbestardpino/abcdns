package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/cloudflare/cloudflare-go"
	"github.com/joho/godotenv"
	"github.com/robfig/cron/v3"
)

type IP struct {
	Query string
}

const ENV_FILE_PATH = ".env"

func main() {

	registerHealthCheckEndpoint()

	if fileExists(ENV_FILE_PATH) {
		log.Println("LOADING ENV FILE")
		err := godotenv.Load(ENV_FILE_PATH)
		if err != nil {
			log.Fatalf("ERR LOADING ENV FILE: %s", err.Error())
		}
	}

	log.Println("CREATING CLOUDFLARE API CLIENT")
	api, err := cloudflare.NewWithAPIToken(os.Getenv("CLOUDFLARE_API_TOKEN"))
	if err != nil {
		log.Fatalf("ERR CREATING CLOUDFLARE API CLIENT: %s", err)
	}

	zoneID, err := api.ZoneIDByName(os.Getenv("CLOUDFLARE_ZONE_NAME"))
	if err != nil {
		log.Fatalf("ERR GETTING CLOUDFLARE ZONE ID: %s", err)
	}

	cloudflareRecordName := os.Getenv("CLOUDFLARE_RECORD_NAME")
	if cloudflareRecordName == "" {
		log.Fatalf("ERR CLOUDFLARE_RECORD_NAME ENV VAR NOT SET")
	}

	cronSchedule := os.Getenv("CRON_SCHEDULE")
	if cronSchedule == "" {
		log.Fatalf("ERR CRON_SCHEDULE ENV VAR NOT SET")
	}

	ctx := context.Background()
	zoneIdentifier := cloudflare.ZoneIdentifier(zoneID)

	log.Println("RUNNING INITIAL DNS RECORD CHECK")
	recs, _, err := api.ListDNSRecords(ctx, zoneIdentifier, cloudflare.ListDNSRecordsParams{
		Type: "A",
		Name: cloudflareRecordName,
	})
	if err != nil {
		log.Fatalf("ERR LISTING DNS RECORDS %s", err)
	}

	var rec cloudflare.DNSRecord
	if len(recs) == 0 {
		ip, err := getPublicIP()
		if err != nil {
			log.Fatalf("ERR GETTING PUBLIC IP: %s", err)
			return
		}

		createdRec, err := api.CreateDNSRecord(ctx, zoneIdentifier, cloudflare.CreateDNSRecordParams{
			Type:    "A",
			Name:    os.Getenv("CLOUDFLARE_RECORD_NAME"),
			Content: ip,
			TTL:     1,
			Proxied: cloudflare.BoolPtr(false), // Disable Cloudflare proxy, this exposes the origin IP address so use with caution
			Comment: "Custom DDNS",
		})
		if err != nil {
			log.Fatalf("ERR CREATING DNS RECORD: %s", err)
		}
		fmt.Println("CREATED DNS RECORD")

		rec = createdRec
	} else {
		rec = recs[0]
	}

	c := cron.New()
	c.AddFunc(cronSchedule, func() {
		log.Println("RUNNING CRON JOB")
		ip, err := getPublicIP()
		if err != nil {
			log.Fatalf("ERR GETTING PUBLIC IP: %s", err)
			return
		}

		if rec.Content != ip {
			newRec, err := api.UpdateDNSRecord(ctx, zoneIdentifier, cloudflare.UpdateDNSRecordParams{
				Content: ip,
				ID:      rec.ID,
			})
			if err != nil {
				log.Fatalf("ERR UPDATING DNS RECORD: %s", err)
			}
			fmt.Println("UPDATED DNS RECORD")

			rec = newRec
		}
	})
	go c.Start()
	defer c.Stop()
	select {}
}

func getPublicIP() (string, error) {
	req, err := http.Get("http://ip-api.com/json/")
	if err != nil {
		return "", err
	}
	defer req.Body.Close()

	body, err := io.ReadAll(req.Body)
	if err != nil {
		return "", err
	}
	var ip IP
	json.Unmarshal(body, &ip)
	return ip.Query, nil
}

func fileExists(filePath string) bool {
	_, error := os.Stat(filePath)
	return !errors.Is(error, os.ErrNotExist)
}

func registerHealthCheckEndpoint() {
	log.Println("REGISTERING HEALTH CHECK ENDPOINT at localhost:8080/health")
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	go http.ListenAndServe(":8080", nil)
}
