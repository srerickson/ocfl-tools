package testutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const envS3Endpoint = "OCFL_TEST_S3"

// S3Enabled returns true if $OCFL_TEST_S3 is set
func S3Enabled() bool { return os.Getenv(envS3Endpoint) != "" }

// S3Endpoint returns the S3 Endpoint that should be used for testing.
func S3Endpoint() string { return os.Getenv(envS3Endpoint) }

// TempS3Location returns a location string ("s3://test-bucket/prefix")
// for a temporary bucket is removed with the test cleanup.
func TempS3Location(t *testing.T, prefix string) string {
	t.Helper()
	ctx := context.Background()
	cli, err := S3Client(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bucket, err := TmpBucket(ctx, cli)
	if err != nil {
		t.Fatal("setting up S3 bucket:", err)
	}
	t.Cleanup(func() {
		if err := RemoveBucket(ctx, cli, bucket); err != nil {
			t.Fatal("cleaning up S3 bucket:", err)
		}
	})
	return "s3://" + bucket + "/" + prefix
}

func S3Client(ctx context.Context) (*s3.Client, error) {
	endpoint := os.Getenv(envS3Endpoint)
	if endpoint == "" {
		return nil, errors.New("S3 not enabled in thest test environment: $OCFL_TEST_S3 not set")
	}
	cnf, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, err
	}
	cli := s3.NewFromConfig(cnf, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})
	return cli, nil
}

func TmpBucket(ctx context.Context, cli *s3.Client) (string, error) {
	var bucket string
	var retries int
	for {
		bucket = randName("ocfl-tools-test")
		_, err := cli.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: &bucket})
		if err == nil {
			break
		}
		if retries > 4 {
			return "", err
		}
		retries++
	}
	return bucket, nil
}

func RemoveBucket(ctx context.Context, s3cl *s3.Client, bucket string) error {
	b := aws.String(bucket)
	listInput := &s3.ListObjectsV2Input{Bucket: b}
	for {
		list, err := s3cl.ListObjectsV2(ctx, listInput)
		if err != nil {
			return err
		}
		for _, obj := range list.Contents {
			_, err = s3cl.DeleteObject(ctx, &s3.DeleteObjectInput{
				Bucket: b,
				Key:    obj.Key,
			})
			if err != nil {
				return err
			}
		}
		listInput.ContinuationToken = list.NextContinuationToken
		if listInput.ContinuationToken == nil {
			break
		}
	}
	_, err := s3cl.DeleteBucket(ctx, &s3.DeleteBucketInput{
		Bucket: aws.String(bucket),
	})
	return err
}

func randName(prefix string) string {
	byt, err := io.ReadAll(io.LimitReader(rand.Reader, 4))
	if err != nil {
		panic("randName: " + err.Error())
	}
	now := time.Now().UnixMicro()
	return fmt.Sprintf("%s-%d-%s", prefix, now, hex.EncodeToString(byt))
}
