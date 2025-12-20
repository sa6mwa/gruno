package importer

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// xmlHelperPrelude returns a small pure-JS XML helper usable in Bru/VM2.
func xmlHelperPrelude() string {
	return `var parseXML = (function(){
  function node(name, children, text) { return {name:name, children:children||[], text:text||''}; }
  function rootOf(n){ return n && n.root ? n.root : n; }
  function norm(seg){ var i=seg.indexOf(':'); return i>=0 ? seg.slice(i+1) : seg; }
  function parse(str){
    var stack=[]; var root=node('root',[],''); var cur=root;
    var re=/<([^>]+)>|([^<]+)/g, m;
    while((m=re.exec(str))!==null){
      if(m[2]){ var t=m[2].trim(); if(t){ cur.text += t; } continue; }
      var tag=m[1];
      if(tag[0]=='?') continue;
      if(tag[0]=='/') { cur=stack.pop()||root; continue; }
      var selfClose=tag.endsWith('/');
      var parts=tag.replace(/\/$/,'').split(/\s+/); var nm=parts[0];
      var child=node(local(nm),[], '');
      cur.children.push(child);
      if(!selfClose){ stack.push(cur); cur=child; }
    }
    root.src = str;
    return root;
  }
  function local(n){ var i=n.indexOf(':'); return i>=0? n.slice(i+1): n; }
  function findAll(n, path, out){
    n = rootOf(n);
    if(path.length===0){ out.push(n); return; }
    var head=norm(path[0]);
    n.children.forEach(function(c){ if(c.name===head){ findAll(c, path.slice(1), out); } });
  }
  function first(n,path){ var out=[]; findAll(n,path,out); return out.length? out[0].text : undefined; }
  function values(n,path){ var out=[]; findAll(n,path,out); return out.map(function(x){return x.text;}); }
  function fallbackRegex(src, tag){
    tag = norm(tag);
    var re = new RegExp('<(?:[A-Za-z0-9_]+:)?'+tag+'>([^<]*)<', 'ig');
    var arr=[]; var m; while((m=re.exec(src))!==null){ arr.push(m[1]); }
    return arr;
  }
  function has(n,path){
    n = rootOf(n);
    return first(n,path)!==undefined || fallbackRegex(n.src||'', path[path.length-1]).length>0;
  }
  function firstWithFallback(n,path){
    n = rootOf(n);
    var f = first(n,path);
    if(f!==undefined) return f;
    var vals = fallbackRegex(n.src||'', path[path.length-1]);
    return vals.length? vals[0]: undefined;
  }
  function valuesWithFallback(n,path){
    n = rootOf(n);
    var v = values(n,path);
    if(v.length) return v;
    return fallbackRegex(n.src||'', path[path.length-1]);
  }
  return function(str){
    var root=parse(str);
    return {
      root:root,
      has:function(p){return has(root,p);},
      first:function(p){return firstWithFallback(root,p);},
      values:function(p){return valuesWithFallback(root,p);}
    };
  };
})();`
}

// ImportWSDL parses a WSDL and generates a Bruno collection with one SOAP request per operation.
func ImportWSDL(ctx context.Context, opts Options) error {
	if opts.Source == "" {
		return fmt.Errorf("--source is required")
	}

	var data []byte
	var err error
	switch {
	case isURL(opts.Source) && opts.Insecure:
		data, err = fetchWithClient(opts.Source, insecureHTTPClient())
	case isURL(opts.Source):
		data, err = fetchWithClient(opts.Source, nil)
	default:
		data, err = os.ReadFile(opts.Source)
	}
	if err != nil {
		return fmt.Errorf("load wsdl: %w", err)
	}

	if !opts.GenerateTestsSet {
		opts.GenerateTests = true
	}

	var def wsdlDefinitions
	if err := xml.Unmarshal(data, &def); err != nil {
		return fmt.Errorf("parse wsdl: %w", err)
	}

	if opts.OutputDir != "" {
		if err := os.MkdirAll(opts.OutputDir, 0o755); err != nil {
			return err
		}
		name := opts.CollectionName
		if name == "" {
			name = def.Name
			if name == "" {
				name = "wsdl-import"
			}
		}
		_ = os.WriteFile(filepath.Join(opts.OutputDir, "bruno.json"), fmt.Appendf(nil, `{"name":%q,"version":"1.0","type":"collection"}`, name), 0o644)
	}

	typeIndex := buildElementIndex(def)

	baseURL := firstAddress(def)
	if baseURL == "" {
		baseURL = "http://example.com/soap"
	}
	if opts.OutputDir != "" {
		envDir := filepath.Join(opts.OutputDir, "environments")
		_ = os.MkdirAll(envDir, 0o755)
		env := fmt.Sprintf("vars {\n  baseUrl: %s\n}\n", baseURL)
		_ = os.WriteFile(filepath.Join(envDir, "local.bru"), []byte(env), 0o644)
	}

	writeFile := func(rel, content string) error {
		if opts.OutputDir == "" {
			return nil
		}
		full := filepath.Join(opts.OutputDir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		return os.WriteFile(full, []byte(content), 0o644)
	}

	for _, b := range def.Bindings {
		dir := sanitizeFileName(b.Name)
		if dir == "" {
			dir = "binding"
		}
		for _, op := range b.Operations {
			opName := op.Name
			if opName == "" {
				opName = "operation"
			}
			action := op.Soap.Action
			envelope := soapEnvelope(def.TargetNamespace, opName)
			testsBlock := ""
			if opts.GenerateTests {
				if tb := buildWSDLTests(opName, typeIndex); tb != "" {
					testsBlock = tb
				} else {
					testsBlock = defaultWSDLTests(opName)
				}
			} else {
				testsBlock = defaultWSDLTests(opName)
			}

			bru := fmt.Sprintf(`meta { name: %s, type: http }

post {
  url: {{baseUrl}}
  headers {
    Content-Type: text/xml
    SOAPAction: "%s"
  }
  body:xml {
%s
  }
}

%s
`, opName, action, indent(envelope, "    "), testsBlock)
			filename := filepath.Join(dir, sanitizeFileName(opName)+".bru")
			if err := writeFile(filename, bru); err != nil {
				return err
			}
		}
	}

	if opts.OutputFile != "" {
		return writeJSONFile(opts.OutputFile, map[string]any{
			"type":    "wsdl",
			"source":  opts.Source,
			"output":  opts.OutputDir,
			"baseUrl": baseURL,
		})
	}
	return nil
}

// Simple WSDL structs (subset).
type wsdlDefinitions struct {
	XMLName         xml.Name      `xml:"definitions"`
	Name            string        `xml:"name,attr"`
	TargetNamespace string        `xml:"targetNamespace,attr"`
	Services        []wsdlService `xml:"service"`
	Bindings        []wsdlBinding `xml:"binding"`
	Types           []xsdSchema   `xml:"types>schema"`
}

type wsdlService struct {
	Name  string        `xml:"name,attr"`
	Ports []wsdlPortRef `xml:"port"`
}

type wsdlPortRef struct {
	Name    string      `xml:"name,attr"`
	Binding string      `xml:"binding,attr"`
	Address wsdlAddress `xml:"address"`
}

type wsdlAddress struct {
	Location string `xml:"location,attr"`
}

type wsdlBinding struct {
	Name       string          `xml:"name,attr"`
	Type       string          `xml:"type,attr"`
	Operations []wsdlBindingOp `xml:"operation"`
}

type wsdlBindingOp struct {
	Name string `xml:"name,attr"`
	Soap struct {
		Action string `xml:"soapAction,attr"`
	} `xml:"operation"`
}

// XSD subset for schema-driven assertions.
type xsdSchema struct {
	TargetNamespace string           `xml:"targetNamespace,attr"`
	Elements        []xsdElement     `xml:"element"`
	SimpleTypes     []xsdSimpleType  `xml:"simpleType"`
	ComplexTypes    []xsdComplexType `xml:"complexType"`
}

type xsdElement struct {
	Name        string          `xml:"name,attr"`
	Type        string          `xml:"type,attr"`
	MinOccurs   string          `xml:"minOccurs,attr"`
	MaxOccurs   string          `xml:"maxOccurs,attr"`
	SimpleType  *xsdSimpleType  `xml:"simpleType"`
	ComplexType *xsdComplexType `xml:"complexType"`
}

type xsdComplexType struct {
	Name     string      `xml:"name,attr"`
	Sequence xsdSequence `xml:"sequence"`
}

type xsdSequence struct {
	Elements []xsdElement `xml:"element"`
}

type xsdSimpleType struct {
	Name        string         `xml:"name,attr"`
	Restriction xsdRestriction `xml:"restriction"`
}

type xsdRestriction struct {
	Base           string     `xml:"base,attr"`
	Enums          []xsdEnum  `xml:"enumeration"`
	Pattern        xsdPattern `xml:"pattern"`
	MinInclusive   []xsdValue `xml:"minInclusive"`
	MaxInclusive   []xsdValue `xml:"maxInclusive"`
	MinExclusive   []xsdValue `xml:"minExclusive"`
	MaxExclusive   []xsdValue `xml:"maxExclusive"`
	MinLengthVals  []xsdValue `xml:"minLength"`
	MaxLengthVals  []xsdValue `xml:"maxLength"`
	TotalDigits    []xsdValue `xml:"totalDigits"`
	FractionDigits []xsdValue `xml:"fractionDigits"`
}

type xsdEnum struct {
	Value string `xml:"value,attr"`
}

type xsdPattern struct {
	Value string `xml:"value,attr"`
}

type xsdValue struct {
	Value string `xml:"value,attr"`
}

func firstAddress(def wsdlDefinitions) string {
	for _, s := range def.Services {
		for _, p := range s.Ports {
			if p.Address.Location != "" {
				return p.Address.Location
			}
		}
	}
	return ""
}

func soapEnvelope(ns, op string) string {
	if ns == "" {
		ns = "http://example.com/ns"
	}
	if op == "" {
		op = "operation"
	}
	return fmt.Sprintf(`<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/" xmlns:ns="%s">
  <soapenv:Header/>
  <soapenv:Body>
    <ns:%s>
      <!-- TODO: add parameters -->
    </ns:%s>
  </soapenv:Body>
</soapenv:Envelope>`, ns, op, op)
}

func indent(s, pad string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = pad + l
	}
	return strings.Join(lines, "\n")
}

func defaultWSDLTests(opName string) string {
	return fmt.Sprintf(`tests {
  test("status ok", function() { expect(res.status).to.equal(200); });
  test("envelope present", function() { expect(res.text()).to.match(/Envelope/i); });
  test("response element", function() { expect(res.text()).to.match(new RegExp("<" + %q)); });
}
`, opName)
}

// buildElementIndex flattens schema elements by name for quick lookup.
func buildElementIndex(def wsdlDefinitions) map[string]xsdElement {
	index := map[string]xsdElement{}
	for _, s := range def.Types {
		for _, el := range s.Elements {
			index[el.Name] = el
		}
		for _, st := range s.SimpleTypes {
			if st.Name != "" {
				index[st.Name] = xsdElement{Name: st.Name, SimpleType: &st}
			}
		}
		for _, ct := range s.ComplexTypes {
			if ct.Name != "" {
				index[ct.Name] = xsdElement{Name: ct.Name, ComplexType: &ct}
			}
		}
	}
	return index
}

func buildWSDLTests(opName string, index map[string]xsdElement) string {
	respEl, ok := index[opName+"Response"]
	if !ok || respEl.ComplexType == nil {
		return defaultWSDLTests(opName)
	}

	asserts := []string{
		xmlHelperPrelude(),
		"var xml = parseXML(res.text());",
		"expect(res.status).to.equal(200);",
		"if (xml.has(['Envelope','Body','Fault'])) { throw new Error('soap fault'); }",
	}

	asserts = append(asserts, buildElementAssertions(respEl, index, 0, []string{respEl.Name}, "")...)

	if len(asserts) == 0 {
		return defaultWSDLTests(opName)
	}
	body := strings.Join(asserts, "\n  ")
	return fmt.Sprintf(`tests {
  test("schema", function() {
  %s
  });
}
`, body)
}

func buildElementAssertions(el xsdElement, index map[string]xsdElement, depth int, path []string, expectedCountVar string) []string {
	if depth > 4 { // prevent runaway recursion
		return nil
	}
	var asserts []string
	tag := el.Name
	if tag == "" {
		return nil
	}
	fullPath := path
	ident := pathIdent(path)

	minCount := occursToInt(el.MinOccurs, 1)
	maxCount := occursToInt(el.MaxOccurs, -1)
	required := minCount > 0
	minExpr := strconv.Itoa(minCount)
	maxExpr := ""
	if maxCount > 0 {
		maxExpr = strconv.Itoa(maxCount)
	}
	if expectedCountVar != "" {
		if minCount > 0 {
			minExpr = fmt.Sprintf("%s * %d", expectedCountVar, minCount)
		}
		if maxCount > 0 {
			maxExpr = fmt.Sprintf("%s * %d", expectedCountVar, maxCount)
		}
	}

	// Track presence once; reused by optional gates.
	hasVar := fmt.Sprintf("has_%s", ident)
	asserts = append(asserts, fmt.Sprintf("var %s = xml.has(%s);", hasVar, jsPath(fullPath)))
	if required {
		asserts = append(asserts, fmt.Sprintf("expect(%s).to.equal(true);", hasVar))
	}

	isArray := maxCount == -1 || maxCount > 1 || minCount > 1
	var arrayVar string
	valuesVar := ""
	if isArray || minCount > 1 || maxCount > 0 || expectedCountVar != "" {
		arrayVar = fmt.Sprintf("arr_%s", ident)
		valuesVar = arrayVar
		asserts = append(asserts, fmt.Sprintf("var %s = xml.values(%s);", arrayVar, jsPath(fullPath)))
		if minCount > 0 {
			asserts = append(asserts, fmt.Sprintf("expect(%s.length).to.be.at.least(%s);", arrayVar, minExpr))
		}
		if maxExpr != "" {
			asserts = append(asserts, fmt.Sprintf("expect(%s.length).to.be.at.most(%s);", arrayVar, maxExpr))
		}
	}

	if valuesVar == "" && (required || expectedCountVar != "") {
		valuesVar = fmt.Sprintf("vals_%s", ident)
		asserts = append(asserts, fmt.Sprintf("var %s = xml.values(%s);", valuesVar, jsPath(fullPath)))
		if expectedCountVar != "" && minCount > 0 {
			asserts = append(asserts, fmt.Sprintf("expect(%s.length).to.be.at.least(%s);", valuesVar, minExpr))
		}
		if expectedCountVar != "" && maxExpr != "" {
			asserts = append(asserts, fmt.Sprintf("expect(%s.length).to.be.at.most(%s);", valuesVar, maxExpr))
		}
	}

	// extract first occurrence value
	asserts = append(asserts, fmt.Sprintf("var m_%s = %s ? xml.first(%s) : undefined;", ident, hasVar, jsPath(fullPath)))
	if required {
		asserts = append(asserts, fmt.Sprintf("expect(m_%s).to.not.equal(undefined);", ident))
	}
	valRef := fmt.Sprintf("m_%s", ident)

	// Collect applicable simple type restriction for reuse (single and array)
	var simple *xsdRestriction
	if el.SimpleType != nil {
		simple = &el.SimpleType.Restriction
	}

	// Named simple or complex type reference
	if el.Type != "" {
		if ref, ok := index[localName(el.Type)]; ok {
			if ref.SimpleType != nil {
				simple = &ref.SimpleType.Restriction
			}
			if ref.ComplexType != nil {
				asserts = append(asserts, buildComplexForEach(ref.ComplexType, index, depth, fullPath, hasVar, arrayVar, expectedCountVar, required)...)
			}
		}
	}
	if simple == nil && el.Type != "" {
		base := localName(el.Type)
		simple = &xsdRestriction{Base: base}
	}

	if simple != nil {
		merged := resolveRestriction(*simple, index)
		valChecks := simpleTypeAsserts(merged, valRef)
		if len(valChecks) > 0 {
			asserts = append(asserts, fmt.Sprintf("if (%s !== undefined) { %s }", valRef, strings.Join(valChecks, " ")))
		}
		if isArray {
			arrayChecks := simpleTypeAsserts(merged, "v")
			if len(arrayChecks) > 0 {
				if arrayVar == "" {
					arrayVar = fmt.Sprintf("arr_%s", ident)
					asserts = append(asserts, fmt.Sprintf("var %s = xml.values(%s);", arrayVar, jsPath(fullPath)))
				}
				asserts = append(asserts, fmt.Sprintf("%s.forEach(function(v){ %s });", arrayVar, strings.Join(arrayChecks, " ")))
			}
		}
	}

	if el.ComplexType != nil {
		asserts = append(asserts, buildComplexForEach(el.ComplexType, index, depth, fullPath, hasVar, arrayVar, expectedCountVar, required)...)
	}

	return asserts
}

func buildComplexForEach(ct *xsdComplexType, index map[string]xsdElement, depth int, path []string, hasVar, arrayVar, expectedCountVar string, required bool) []string {
	child := complexTypeAsserts(ct, index, depth+1, path)
	if len(child) == 0 {
		return nil
	}

	// per-occurrence: if array, iterate; else single check gated by presence when optional
	if arrayVar != "" {
		return []string{fmt.Sprintf("%s.forEach(function(_, idx){ %s });", arrayVar, strings.Join(child, " "))}
	}

	if required || hasVar == "" {
		return child
	}
	return []string{fmt.Sprintf("if (%s) { %s }", hasVar, strings.Join(child, " "))}
}

func complexTypeAsserts(ct *xsdComplexType, index map[string]xsdElement, depth int, path []string) []string {
	var asserts []string
	for _, child := range ct.Sequence.Elements {
		asserts = append(asserts, buildElementAssertions(child, index, depth+1, append(path, child.Name), "")...)
	}
	return asserts
}

func resolveRestriction(r xsdRestriction, index map[string]xsdElement) xsdRestriction {
	seen := map[string]bool{}
	for {
		base := localName(r.Base)
		if base == "" || seen[base] {
			return r
		}
		seen[base] = true
		baseEl, ok := index[base]
		if !ok || baseEl.SimpleType == nil {
			return r
		}
		parent := baseEl.SimpleType.Restriction
		r = mergeRestrictions(parent, r)
		if parent.Base != "" {
			r.Base = parent.Base
		}
	}
}

func mergeRestrictions(base, child xsdRestriction) xsdRestriction {
	out := child
	if out.Enums == nil {
		out.Enums = base.Enums
	}
	if out.Pattern.Value == "" {
		out.Pattern = base.Pattern
	}
	if len(out.MinLengthVals) == 0 {
		out.MinLengthVals = base.MinLengthVals
	}
	if len(out.MaxLengthVals) == 0 {
		out.MaxLengthVals = base.MaxLengthVals
	}
	if len(out.MinInclusive) == 0 {
		out.MinInclusive = base.MinInclusive
	}
	if len(out.MaxInclusive) == 0 {
		out.MaxInclusive = base.MaxInclusive
	}
	if len(out.MinExclusive) == 0 {
		out.MinExclusive = base.MinExclusive
	}
	if len(out.MaxExclusive) == 0 {
		out.MaxExclusive = base.MaxExclusive
	}
	if len(out.TotalDigits) == 0 {
		out.TotalDigits = base.TotalDigits
	}
	if len(out.FractionDigits) == 0 {
		out.FractionDigits = base.FractionDigits
	}
	if out.Base == "" {
		out.Base = base.Base
	}
	return out
}

func simpleTypeAsserts(r xsdRestriction, valRef string) []string {
	var asserts []string
	base := localName(r.Base)
	byteLen := func() string {
		return fmt.Sprintf("(function(s){var p=(s.match(/=*$/)||[''])[0].length; return Math.floor((s||'').length*3/4)-p;})(%s)", valRef)
	}
	hexByteLen := func() string {
		return fmt.Sprintf("(%s||'').length/2", valRef)
	}

	// base type checks
	switch base {
	case "int", "integer", "decimal", "float", "double", "long", "short", "byte", "unsignedInt", "unsignedShort", "unsignedLong", "unsignedByte":
		asserts = append(asserts, fmt.Sprintf("expect(!isNaN(parseFloat(%s))).to.equal(true);", valRef))
		if strings.HasPrefix(base, "unsigned") || base == "nonNegativeInteger" || base == "positiveInteger" {
			asserts = append(asserts, fmt.Sprintf("expect(parseFloat(%s)).to.be.at.least(0);", valRef))
		}
		if base == "positiveInteger" {
			asserts = append(asserts, fmt.Sprintf("expect(parseFloat(%s)).to.be.above(0);", valRef))
		}
		if base == "nonPositiveInteger" {
			asserts = append(asserts, fmt.Sprintf("expect(parseFloat(%s)).to.be.at.most(0);", valRef))
		}
		if base == "negativeInteger" {
			asserts = append(asserts, fmt.Sprintf("expect(parseFloat(%s)).to.be.below(0);", valRef))
		}
	case "boolean":
		asserts = append(asserts, fmt.Sprintf("expect(%s === 'true' || %s === 'false').to.equal(true);", valRef, valRef))
	case "base64Binary":
		asserts = append(asserts, fmt.Sprintf("expect(/^(?:[A-Za-z0-9+\\/]{4})*(?:[A-Za-z0-9+\\/]{2}==|[A-Za-z0-9+\\/]{3}=)?$/.test(%s)).to.equal(true);", valRef))
		asserts = append(asserts, fmt.Sprintf("expect(%s).to.be.greaterThan(0);", byteLen()))
	case "hexBinary":
		asserts = append(asserts, fmt.Sprintf("expect(/^[0-9A-Fa-f]+$/.test(%s)).to.equal(true);", valRef))
		asserts = append(asserts, fmt.Sprintf("expect(%s).to.be.greaterThan(0);", hexByteLen()))
	case "date":
		asserts = append(asserts, fmt.Sprintf("expect(/^\\d{4}-\\d{2}-\\d{2}$/.test(%s)).to.equal(true);", valRef))
	case "dateTime":
		asserts = append(asserts, fmt.Sprintf("expect(/^\\d{4}-\\d{2}-\\d{2}T\\d{2}:\\d{2}:\\d{2}(?:\\.\\d+)?(?:Z|[+-]\\d{2}:?\\d{2})?$/.test(%s)).to.equal(true);", valRef))
	case "time":
		asserts = append(asserts, fmt.Sprintf("expect(/^\\d{2}:\\d{2}:\\d{2}(?:\\.\\d+)?(?:Z|[+-]\\d{2}:?\\d{2})?$/.test(%s)).to.equal(true);", valRef))
	case "anyURI":
		asserts = append(asserts, fmt.Sprintf("expect(/^[A-Za-z][A-Za-z0-9+.-]*:/.test(%s)).to.equal(true);", valRef))
	}

	if len(r.Enums) > 0 {
		vals := make([]string, 0, len(r.Enums))
		for _, e := range r.Enums {
			vals = append(vals, fmt.Sprintf("%q", e.Value))
		}
		asserts = append(asserts, fmt.Sprintf("expect([%s]).to.include(%s);", strings.Join(vals, ","), valRef))
	}
	if r.Pattern.Value != "" {
		asserts = append(asserts, fmt.Sprintf("expect(/%s/.test(%s)).to.equal(true);", jsRegexLiteral(r.Pattern.Value), valRef))
	}
	if len(r.MinLengthVals) > 0 {
		if base == "base64Binary" {
			asserts = append(asserts, fmt.Sprintf("expect(%s).to.be.at.least(%s);", byteLen(), r.MinLengthVals[0].Value))
		} else if base == "hexBinary" {
			asserts = append(asserts, fmt.Sprintf("expect(%s).to.be.at.least(%s);", hexByteLen(), r.MinLengthVals[0].Value))
		} else {
			asserts = append(asserts, fmt.Sprintf("expect((%s||'').length).to.be.at.least(%s);", valRef, r.MinLengthVals[0].Value))
		}
	}
	if len(r.MaxLengthVals) > 0 {
		if base == "base64Binary" {
			asserts = append(asserts, fmt.Sprintf("expect(%s).to.be.at.most(%s);", byteLen(), r.MaxLengthVals[0].Value))
		} else if base == "hexBinary" {
			asserts = append(asserts, fmt.Sprintf("expect(%s).to.be.at.most(%s);", hexByteLen(), r.MaxLengthVals[0].Value))
		} else {
			asserts = append(asserts, fmt.Sprintf("expect((%s||'').length).to.be.at.most(%s);", valRef, r.MaxLengthVals[0].Value))
		}
	}
	if len(r.MinInclusive) > 0 {
		asserts = append(asserts, fmt.Sprintf("expect(parseFloat(%s)).to.be.at.least(%s);", valRef, r.MinInclusive[0].Value))
	}
	if len(r.MaxInclusive) > 0 {
		asserts = append(asserts, fmt.Sprintf("expect(parseFloat(%s)).to.be.at.most(%s);", valRef, r.MaxInclusive[0].Value))
	}
	if len(r.MinExclusive) > 0 {
		asserts = append(asserts, fmt.Sprintf("expect(parseFloat(%s)).to.be.greaterThan(%s);", valRef, r.MinExclusive[0].Value))
	}
	if len(r.MaxExclusive) > 0 {
		asserts = append(asserts, fmt.Sprintf("expect(parseFloat(%s)).to.be.below(%s);", valRef, r.MaxExclusive[0].Value))
	}
	if len(r.TotalDigits) > 0 {
		asserts = append(asserts, fmt.Sprintf("expect(%s.replace(/[^0-9]/g,'').length).to.be.at.most(%s);", valRef, r.TotalDigits[0].Value))
	}
	if len(r.FractionDigits) > 0 {
		asserts = append(asserts, fmt.Sprintf("expect((%s.split('.')[1]||'').length).to.be.at.most(%s);", valRef, r.FractionDigits[0].Value))
	}
	return asserts
}

func occursToInt(s string, def int) int {
	if s == "" {
		return def
	}
	if s == "unbounded" {
		return -1
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}

// jsRegexLiteral escapes a pattern for inline `/.../` JavaScript regexes.
func jsRegexLiteral(p string) string {
	// Collapse doubly-escaped backslashes (common when patterns are authored for JSON/YAML then carried into XSD).
	p = strings.ReplaceAll(p, "\\\\", "\\")
	return strings.ReplaceAll(p, "/", `\/`)
}

func jsPath(path []string) string {
	parts := make([]string, len(path))
	for i, p := range path {
		parts[i] = fmt.Sprintf("%q", p)
	}
	return fmt.Sprintf("[%s]", strings.Join(parts, ","))
}

func pathIdent(path []string) string {
	return strings.ReplaceAll(strings.Join(path, "_"), "-", "_")
}

func localName(qname string) string {
	if _, after, ok := strings.Cut(qname, ":"); ok {
		return after
	}
	return qname
}
