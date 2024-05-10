package elasticsearch

import (
	"context"
	"crypto/tls"
	"log"
	"net/http"
	"time"

	"github.com/buildbarn/bb-storage/pkg/util"
	"github.com/elastic/go-elasticsearch/v8"
	pb "github.com/meroton/buildbar/proto/configuration/elasticsearch"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// NewClientFromConfiguration creates an elasticsearch.TypedClient object
// based on the provided configuration.
func NewClientFromConfiguration(configuration *pb.ClientConfiguration) (*elasticsearch.TypedClient, error) {
	if configuration == nil {
		return nil, status.Error(codes.InvalidArgument, "No Elasticsearch client configuration provided")
	}
	elasticsearchConfig := elasticsearch.Config{
		Addresses: configuration.Addresses,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: configuration.SkipVerifyTls},
		},
	}

	switch credentials := configuration.Credentials.Credentials.(type) {
	case *pb.ClientCredentials_BasicAuth:
		elasticsearchConfig.Username = credentials.BasicAuth.Username
		elasticsearchConfig.Password = credentials.BasicAuth.Password
	case *pb.ClientCredentials_ApiKey:
		elasticsearchConfig.APIKey = credentials.ApiKey
	case *pb.ClientCredentials_ServiceToken:
		elasticsearchConfig.ServiceToken = credentials.ServiceToken
	default:
		return nil, status.Error(codes.InvalidArgument,
			"Configuration did not contain a supported Elasticsearch credentials type")
	}

	client, err := elasticsearch.NewTypedClient(elasticsearchConfig)
	if err != nil {
		return nil, util.StatusWrap(err, "Failed to create Elasticsearch client")
	}
	return client, nil
}

// DieWhenConnectionFails checks periodically that Elasticsearch can be
// reached. If this fails after a few retries, it panics.
//
// TODO: Patch bb-storage to allow for dynamic health status.
func DieWhenConnectionFails(ctx context.Context, es *elasticsearch.TypedClient) {
	retries := -1
	lastSuccess := time.Now()
	for {
		res, err := es.Info().Do(ctx)
		if err != nil {
			log.Printf("Failed to get Elasticsearch server info: %s", err)
			retries++
			downDuration := time.Since(lastSuccess)
			if retries > 5 && downDuration.Seconds() > 60 {
				log.Fatalf("Elasticsearch connection failed for %s with %d retries", downDuration, retries)
			}
		} else {
			if retries != 0 {
				log.Printf("Elasticsearch info: %#v\n", res)
				retries = 0
				lastSuccess = time.Now()
			}
		}
		time.Sleep(10 * time.Second)
	}
}
