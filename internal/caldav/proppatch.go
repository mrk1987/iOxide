package caldav

import (
	"encoding/xml"
	"net/http"
)

// Apple's Calendar app sends a PROPPATCH while adding an account (to set the
// calendar colour and display name). go-webdav answers PROPPATCH with 501 Not
// Implemented, which makes the app refuse the account. Proton does not let a
// client change those properties anyway, so the proxy accepts the request
// and reports every property as set without storing anything.

type propertyUpdate struct {
	XMLName xml.Name        `xml:"DAV: propertyupdate"`
	Set     []propContainer `xml:"set"`
	Remove  []propContainer `xml:"remove"`
}

type propContainer struct {
	Prop struct {
		Names []propName `xml:",any"`
	} `xml:"prop"`
}

// propName captures just the element name of a property, in its namespace.
type propName struct {
	XMLName xml.Name
}

type multiStatus struct {
	XMLName  xml.Name `xml:"DAV: multistatus"`
	Response struct {
		Href     string `xml:"DAV: href"`
		PropStat struct {
			Prop struct {
				Names []propName `xml:",any"`
			} `xml:"DAV: prop"`
			Status string `xml:"DAV: status"`
		} `xml:"DAV: propstat"`
	} `xml:"DAV: response"`
}

type propPatchHandler struct {
	next http.Handler
}

func withPropPatch(next http.Handler) http.Handler {
	return &propPatchHandler{next: next}
}

func (h *propPatchHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "PROPPATCH" {
		h.next.ServeHTTP(w, r)
		return
	}

	var update propertyUpdate
	if err := xml.NewDecoder(r.Body).Decode(&update); err != nil {
		http.Error(w, "caldav: malformed propertyupdate: "+err.Error(), http.StatusBadRequest)
		return
	}

	var ms multiStatus
	ms.Response.Href = r.URL.Path
	ms.Response.PropStat.Status = "HTTP/1.1 200 OK"
	for _, container := range append(update.Set, update.Remove...) {
		ms.Response.PropStat.Prop.Names = append(ms.Response.PropStat.Prop.Names, container.Prop.Names...)
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	w.Write([]byte(xml.Header))
	xml.NewEncoder(w).Encode(&ms)
}
