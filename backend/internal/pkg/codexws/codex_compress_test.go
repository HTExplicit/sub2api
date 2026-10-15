package websocket

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"testing"

	"github.com/klauspost/compress/flate"
)

func TestCodexBoundedWindowNegotiationAndCompression(t *testing.T) {
	for _, bits := range []int{8, 10, 15} {
		t.Run(strconv.Itoa(bits), func(t *testing.T) {
			offer := &compressionOptions{offerClientWindow: true}
			if offer.String() != "permessage-deflate; client_max_window_bits" {
				t.Fatal(offer.String())
			}
			options, err := verifyServerExtensions(offer, http.Header{"Sec-Websocket-Extensions": {"permessage-deflate; client_max_window_bits=" + strconv.Itoa(bits)}})
			if err != nil || options.clientWindowBits != bits {
				t.Fatalf("negotiation: %+v %v", options, err)
			}
			input := bytes.Repeat([]byte("bounded Codex websocket window "), 4096)
			var wire bytes.Buffer
			writer := getFlateWriter(&wire, bits)
			if _, err = writer.Write(input); err != nil {
				t.Fatal(err)
			}
			if err = writer.Flush(); err != nil {
				t.Fatal(err)
			}
			// RFC 7692 stream tail, followed by the final empty DEFLATE block.
			wire.Write([]byte{1, 0, 0, 255, 255})
			reader := flate.NewReader(bytes.NewReader(wire.Bytes()))
			decoded, err := io.ReadAll(reader)
			_ = reader.Close()
			if err != nil || !bytes.Equal(input, decoded) {
				t.Fatalf("compressed round trip: %v", err)
			}
			putFlateWriter(writer, bits)
		})
	}
	for _, value := range []string{"7", "16", "invalid"} {
		_, err := verifyServerExtensions(&compressionOptions{offerClientWindow: true}, http.Header{"Sec-Websocket-Extensions": {"permessage-deflate; client_max_window_bits=" + value}})
		if err == nil {
			t.Fatalf("accepted invalid window %s", value)
		}
	}
	if _, err := verifyServerExtensions(&compressionOptions{}, http.Header{"Sec-Websocket-Extensions": {"permessage-deflate; client_max_window_bits=10"}}); err == nil {
		t.Fatal("accepted an unoffered window parameter")
	}
}
