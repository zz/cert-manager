// Package dnspod implements a DNS provider for solving the DNS-01 challenge
// using dnspod DNS.
package dnspod

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/decker502/dnspod-go"
	"github.com/jetstack/cert-manager/pkg/issuer/acme/dns/util"
)

// DNSProvider is an implementation of the acme.ChallengeProvider interface.
type DNSProvider struct {
	client *dnspod.Client
}

// NewDNSProvider returns a DNSProvider instance configured for dnspod.
// Credentials must be passed in the environment variables: DNSPOD_API_KEY.
func NewDNSProvider() (*DNSProvider, error) {
	key := os.Getenv("DNSPOD_API_KEY")
	return NewDNSProviderCredentials(key)
}

// NewDNSProviderCredentials uses the supplied credentials to return a
// DNSProvider instance configured for dnspod.
func NewDNSProviderCredentials(key string) (*DNSProvider, error) {
	if key == "" {
		return nil, fmt.Errorf("dnspod credentials missing")
	}

	params := dnspod.CommonParams{LoginToken: key, Format: "json"}
	return &DNSProvider{
		client: dnspod.NewClient(params),
	}, nil
}

// Present creates a TXT record to fulfil the dns-01 challenge.
func (c *DNSProvider) Present(domain, token, keyAuth string) error {
	fqdn, value, ttl := util.DNS01Record(domain, keyAuth)
	zoneID, zoneName, err := c.getHostedZone(domain)
	if err != nil {
		return err
	}

	statusCode := "1"

	recordAttributes := c.newTxtRecord(zoneName, fqdn, value, ttl)
	_, _, statusCode, err = c.client.Domains.CreateRecord(zoneID, *recordAttributes)

	log.Printf("!! CreateRecord status code is: %s", statusCode)

	if statusCode == "104" || statusCode == "25" {
		log.Printf("!! Delete ACME TXT record: %s", domain)
		time.Sleep(5 * time.Second)
		cErr := c.CleanUp(domain, token, keyAuth)
		if cErr != nil {
			log.Printf("** Cleanup DNSPod record failed: %v", cErr)
		}
	}

	if err != nil {
		return fmt.Errorf("dnspod API call failed: %v", err)
	}

	return nil
}

// CleanUp removes the TXT record matching the specified parameters.
func (c *DNSProvider) CleanUp(domain, token, keyAuth string) error {
	fqdn, _, _ := util.DNS01Record(domain, keyAuth)

	records, err := c.findTxtRecords(domain, fqdn)
	if err != nil {
		return err
	}

	zoneID, _, err := c.getHostedZone(domain)
	if err != nil {
		return err
	}
	log.Printf("-- Try to delete DNSPod domain %s TXT records", domain)

	for _, rec := range records {
		log.Printf("!!! Delete DNSPod record ID: %s for domain %s", rec.ID, rec.Name)
		_, err := c.client.Domains.DeleteRecord(zoneID, rec.ID)
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *DNSProvider) getHostedZone(domain string) (string, string, error) {
	zones, _, err := c.client.Domains.List()
	if err != nil {
		return "", "", fmt.Errorf("dnspod API call failed: %v", err)
	}

	authZone, err := util.FindZoneByFqdn(util.ToFqdn(domain), util.RecursiveNameservers)
	if err != nil {
		return "", "", err
	}

	var hostedZone dnspod.Domain
	for _, zone := range zones {
		if zone.Name == util.UnFqdn(authZone) {
			hostedZone = zone
		}
	}

	if hostedZone.ID == 0 {
		return "", "", fmt.Errorf("Zone %s not found in dnspod for domain %s", authZone, domain)

	}

	return fmt.Sprintf("%v", hostedZone.ID), hostedZone.Name, nil
}

func (c *DNSProvider) newTxtRecord(zone, fqdn, value string, ttl int) *dnspod.Record {
	name := c.extractRecordName(fqdn, zone)

	return &dnspod.Record{
		Type:  "TXT",
		Name:  name,
		Value: value,
		Line:  "默认",
		TTL:   "600",
	}
}

func (c *DNSProvider) findTxtRecords(domain, fqdn string) ([]dnspod.Record, error) {
	zoneID, zoneName, err := c.getHostedZone(domain)
	if err != nil {
		return nil, err
	}

	recordName := c.extractRecordName(fqdn, zoneName)

	var records []dnspod.Record
	result, _, err := c.client.Domains.ListRecords(zoneID, recordName)
	if err != nil {
		return records, fmt.Errorf("dnspod API call has failed: %v", err)
	}

	for _, record := range result {
		log.Printf("-- findTxtRecord extractRecordName: %s, listRecord.Name: %s", recordName, record.Name)
		if record.Name == recordName {
			records = append(records, record)
		}
	}

	return records, nil
}

func (c *DNSProvider) extractRecordName(fqdn, domain string) string {
	name := util.UnFqdn(fqdn)
	if idx := strings.Index(name, "."+domain); idx != -1 {
		return name[:idx]
	}
	return name
}

func (c *DNSProvider) Timeout() (timeout, interval time.Duration) {
	return 120 * time.Second, 2 * time.Second
}
