package importer

import (
	"testing"

	"github.com/dop251/goja"
)

func TestXMLHelperPreludeParsesNamespacesAndArrays(t *testing.T) {
	vm := goja.New()
	script := xmlHelperPrelude() + `
var xml = parseXML('<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/" xmlns:ns="http://x/">' +
  '<soap:Body><ns:Res><ns:items>1</ns:items><ns:items>2</ns:items><ns:msg>Hello</ns:msg></ns:Res></soap:Body></soap:Envelope>');
if (!xml.has(['Envelope','Body','Res','msg'])) { throw new Error('has failed'); }
if (xml.first(['Envelope','Body','Res','msg']) !== 'Hello') { throw new Error('first mismatch'); }
var items = xml.values(['Envelope','Body','Res','items']);
if (items.length !== 2 || items[0] !== '1' || items[1] !== '2') { throw new Error('values mismatch'); }
// namespace-aware path segments should work when prefixed
if (!xml.has(['soap:Envelope','soap:Body','ns:Res','ns:msg'])) { throw new Error('ns has failed'); }
if (xml.first(['ns:Res','ns:msg']) !== 'Hello') { throw new Error('ns first mismatch'); }
true;
`
	v, err := vm.RunString(script)
	if err != nil {
		t.Fatalf("run helper: %v", err)
	}
	if b := v.ToBoolean(); !b {
		t.Fatalf("expected true result")
	}
}
