// Copyright (c) 2013, SmugMug, Inc. All rights reserved.
// 
// Redistribution and use in source and binary forms, with or without
// modification, are permitted provided that the following conditions are
// met:
//     * Redistributions of source code must retain the above copyright
//       notice, this list of conditions and the following disclaimer.
//     * Redistributions in binary form must reproduce the above
//       copyright notice, this list of conditions and the following
//       disclaimer in the documentation and/or other materials provided
//       with the distribution.
// 
// THIS SOFTWARE IS PROVIDED BY SMUGMUG, INC. ``AS IS'' AND ANY
// EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
// IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR
// PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL SMUGMUG, INC. BE LIABLE FOR
// ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
// DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE
// GOODS OR SERVICES;LOSS OF USE, DATA, OR PROFITS; OR BUSINESS
// INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER
// IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR
// OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF
// ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

// Implements the wrapper for versioned retryable DynamoDB requests.
// See the init() function below for details about initial conf file processing.
package authreq

import (
	"errors"
	"net/http"
	"fmt"
	"bytes"
	"strings"
	"time"
	"math"
	"log"
	"math/rand"
	"encoding/json"
	"github.com/smugmug/godynamo/auth_v4"
	"github.com/smugmug/godynamo/aws_const"
	ep "github.com/smugmug/godynamo/endpoint"
)

const (
	// auth version numbers
	AUTH_V2 = 2
	AUTH_V4 = 4
)

// Stipulate the current authorization version.
var AUTH_VERSION = AUTH_V4

// RetryReq_V4 sends a retry-able request using an ep.Endpoint structure and v4 auth.
func RetryReq_V4(v ep.Endpoint,amzTarget string) (string,int,error) {
	return retryReq(v,amzTarget)
}

// RetryReq_V4 sends a retry-able request using a JSON serialized request and v4 auth.
func RetryReqJSON_V4(reqJSON []byte,amzTarget string) (string,int,error) {
	return retryReq(reqJSON,amzTarget)
}

// Implement exponential backoff for the req above in the case of 5xx errors
// from aws. Algorithm is lifted from AWS docs.
func retryReq(v interface{},amzTarget string) (string,int,error) {
	resp_body,amz_requestid,code,resp_err := auth_v4.Req(v,amzTarget)
	shouldRetry := false
	if resp_err != nil {
		e := fmt.Sprintf("authreq.RetryReq:0 " +
			" try AuthReq Fail:%s (reqid:%s)",resp_err.Error(),amz_requestid)
		log.Printf("authreq.RetryReq: call err %s\n",e)
		shouldRetry = true
	}
	// see:
	// http://docs.aws.amazon.com/amazondynamodb/latest/developerguide/ErrorHandling.html
	if code >= http.StatusInternalServerError {
		shouldRetry = true // all 5xx codes are deemed retryable by amazon
	}
	if code == http.StatusBadRequest {
		if strings.Contains(resp_body,aws_const.EXCEEDED_MSG) {
			log.Printf("authreq.RetryReq THROUGHPUT WARNING RETRY\n")
			shouldRetry = true
		} else if strings.Contains(resp_body,aws_const.UNRECOGNIZED_CLIENT_MSG) {
			log.Printf("authreq.RetryReq THROUGHPUT WARNING RETRY\n")
			shouldRetry = true
		} else if strings.Contains(resp_body,aws_const.THROTTLING_MSG) {
			log.Printf("authreq.RetryReq THROUGHPUT WARNING RETRY\n")
			shouldRetry = true
		} else {
			v_json,v_json_err := json.Marshal(v)
			if v_json_err == nil {
				var buf bytes.Buffer
				if i_err := json.Indent(&buf,v_json,"","\t"); i_err == nil {
					log.Printf("authreq.RetryReq un-retryable err: %s\n%s\n",
						resp_body,buf.String())
				} else {
					log.Printf("authreq.RetryReq un-retryable err: %s\n%s\n",
						resp_body,string(v_json))
				}
			} else {
				log.Printf("authreq.RetryReq un-retryable err: %s (reqid:%s)\n",resp_body,amz_requestid)
			}
			shouldRetry = false
		}
	}
	if !shouldRetry {
		// not retryable
		return resp_body,code,resp_err
	} else {
		// retry the request RETRIES time in the case of a 5xx
		// response, with an exponentially decayed sleep interval

		// seed our rand number generator g
		g := rand.New(rand.NewSource(time.Now().UnixNano()))
		for i := 1; i<aws_const.RETRIES; i++ {
			// get random delay from range
			// [0..4**i*100 ms)
			log.Printf("authreq.RetryReq: BEGIN SLEEP %v (code:%v) (REQ:%v) (reqid:%s)",time.Now(),code,v,amz_requestid)
			r := time.Millisecond *
				time.Duration(g.Int63n(int64(
				math.Pow(4,float64(i))) *
				100))
			time.Sleep(r)
			log.Printf("authreq.RetryReq END SLEEP %v\n",time.Now())
			shouldRetry = false
			resp_body,amz_requestid,code,resp_err := auth_v4.Req(v,amzTarget)
			if resp_err != nil {
				_ = fmt.Sprintf("authreq.RetryReq:1 " +
					" try AuthReq Fail:%s (reqid:%s)",resp_err.Error(),amz_requestid)
				shouldRetry = true
			}
			if code >= http.StatusInternalServerError {
				shouldRetry = true
			}
			if code == http.StatusBadRequest {
				if strings.Contains(resp_body,aws_const.EXCEEDED_MSG) {
					log.Printf("authreq.RetryReq THROUGHPUT WARNING RETRY\n")
					shouldRetry = true
				}
			}
			if !shouldRetry {
				// worked! no need to retry
				log.Printf("authreq.RetryReq RETRY LOOP SUCCESS")
				return resp_body,code,resp_err
			}
		}
		e := fmt.Sprintf("authreq.RetryReq: failed retries on %s:%v",
			amzTarget,v)
		return "",0,errors.New(e)
	}
}
