// THIS FILE IS AUTOMATICALLY GENERATED. DO NOT EDIT.

package cloudfrontiface_test

import (
	"testing"

	"github.com/awslabs/aws-sdk-go/service/cloudfront"
	"github.com/awslabs/aws-sdk-go/service/cloudfront/cloudfrontiface"
	"github.com/stretchr/testify/assert"
)

func TestInterface(t *testing.T) {
	assert.Implements(t, (*cloudfrontiface.CloudFrontAPI)(nil), cloudfront.New(nil))
}
