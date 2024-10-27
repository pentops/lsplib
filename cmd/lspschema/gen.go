package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/ettle/strcase"
)

func main() {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := run(ctx); err != nil {
		log.Fatal(err)
	}
}

const metaModelURL = "https://raw.githubusercontent.com/microsoft/vscode-languageserver-node/refs/heads/main/protocol/metaModel.json"

func run(ctx context.Context) error {
	model, err := fetchMetaModel(ctx)
	if err != nil {
		return err
	}

	diag, err := model.Structure("Diagnostic")
	if err != nil {
		return err
	}
	pp := newPrinter(os.Stdout)
	pp.PrintStruct(diag)

	return nil
}

type pp struct {
	out io.Writer
}

func newPrinter(out io.Writer) *pp {
	return &pp{out: out}
}

func (p *pp) p(format string, args ...interface{}) {
	_, err := p.out.Write([]byte(fmt.Sprintf(format, args...)))
	if err != nil {
		panic(err)
	}
}

func (p *pp) PrintStruct(s *Structure) {
	types := []string{}
	for _, prop := range s.Properties {
		key := strcase.ToGoPascal(prop.Name)
		var typeName string

		switch prop.Type.Kind {

		case "reference":
			ref := prop.Type.Reference.Found
			if ref == nil {
				panic("ref not found: " + prop.Type.Reference.Name)
			}
			if ref.Structure != nil {
				p.PrintStruct(ref.Structure)
				typeName = "*" + ref.Structure.Name
			} else if ref.Enumeration != nil {
				typeName = ref.Enumeration.Name
			} else if ref.TypeAlias != nil {
				typeName = ref.TypeAlias.Name
			} else {
				panic("not implemented")
			}

		case "base":
			switch prop.Type.Base.Name {
			case "string":
				typeName = "string"
			}
		}
		types = append(types, fmt.Sprintf("%s %s `json:\"%s,omitempty\"", key, typeName, prop.Name))

	}
	p.p("type %s struct {\n", s.Name)
	for _, t := range types {
		p.p("\t%s\n", t)
	}
	p.p("}\n")

}

func fetchMetaModel(ctx context.Context) (*Model, error) {
	b, err := httpGet(ctx, metaModelURL)
	if err != nil {
		return nil, err
	}
	model := &Model{}
	return model, unmarshalStrict(b, &model)
}

func httpGet(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	return b, nil

}

type Model struct {
	MetaData      MetaData      `json:"metaData"`
	Requests      []Request     `json:"requests"`
	Structures    []Structure   `json:"structures"`
	Enumerations  []Enumeration `json:"enumerations"`
	Notifications []Request     `json:"notifications"`
	TypeAliases   []TypeAlias   `json:"typeAliases"`
}

func (m *Model) ResolveRefs(s *Schema) error {
	if s.Reference != nil {
		found, err := m.AnyRef(s.Reference.Name)
		if err != nil {
			return err
		}
		s.Reference.Found = found
		return nil
	}

	if s.Array != nil {
		return m.ResolveRefs(s.Array.Element)
	}

	if s.Or != nil {
		for _, item := range s.Or.Items {
			if err := m.ResolveRefs(item); err != nil {
				return err
			}
		}
	}

	if s.And != nil {
		for _, item := range s.And.Items {
			if err := m.ResolveRefs(item); err != nil {
				return err
			}
		}
	}

	if s.Map != nil {
		if err := m.ResolveRefs(s.Map.Key); err != nil {
			return err
		}
		if err := m.ResolveRefs(s.Map.Value); err != nil {
			return err
		}
	}

	if s.Tuple != nil {
		for _, item := range s.Tuple.Items {
			if err := m.ResolveRefs(item); err != nil {
				return err
			}
		}
	}

	return nil
}

type AnyRef struct {
	Name        string
	Structure   *Structure
	Enumeration *Enumeration
	TypeAlias   *TypeAlias
}

func (m *Model) AnyRef(name string) (*AnyRef, error) {
	for _, s := range m.Structures {
		if s.Name == name {
			structure, err := m.Structure(name)
			if err != nil {
				return nil, err
			}
			return &AnyRef{Name: name, Structure: structure}, nil
		}
	}

	for _, e := range m.Enumerations {
		if e.Name == name {
			return &AnyRef{Name: name, Enumeration: &e}, nil
		}
	}

	for _, a := range m.TypeAliases {
		if a.Name == name {
			return &AnyRef{Name: name, TypeAlias: &a}, nil
		}
	}

	return nil, fmt.Errorf("ref not found: %s", name)

}

func (m *Model) Structure(name string) (*Structure, error) {

	var found *Structure
	for _, s := range m.Structures {
		if s.Name == name {
			found = &s
			break
		}
	}
	if found == nil {
		return nil, fmt.Errorf("structure not found: %s", name)
	}

	for _, prop := range found.Properties {
		if err := m.ResolveRefs(prop.Type); err != nil {
			return nil, err
		}
	}
	return found, nil
}

type MetaData struct {
	Version string `json:"version"`
}

type BaseElement struct {
	Since         *string  `json:"since,omitempty"`
	Proposed      bool     `json:"proposed,omitempty"`
	Deprecated    *string  `json:"deprecated,omitempty"`
	Documentation *string  `json:"documentation,omitempty"`
	SinceTags     []string `json:"sinceTags,omitempty"`
}

type TypeAlias struct {
	BaseElement
	Name string  `json:"name"`
	Type *Schema `json:"type"`
}

type Structure struct {
	BaseElement
	Name           string     `json:"name"`
	Properties     []Property `json:"properties"`
	Documentations string     `json:"documentation"`
	Extends        []*Schema  `json:"extends,omitempty"`
	Mixins         []*Schema  `json:"mixins,omitempty"`
}

type Property struct {
	BaseElement
	Name     string  `json:"name"`
	Type     *Schema `json:"type"`
	Optional bool    `json:"optional"`
}

type Enumeration struct {
	BaseElement
	Name                 string        `json:"name"`
	Type                 *Schema       `json:"type"`
	Values               []interface{} `json:"values"`
	SupportsCustomValues bool          `json:"supportsCustomValues"`
}

type Request struct {
	BaseElement
	Method              string           `json:"method"`
	TypeName            string           `json:"typeName"`
	Result              *Schema          `json:"result"`
	MessageDirection    MessageDirection `json:"messageDirection"`
	Params              *Schema          `json:"params"`
	PartialResult       *Schema          `json:"partialResult"`
	RegistrationOptions *Schema          `json:"registrationOptions"`
	RegistrationMethod  string           `json:"registrationMethod,omitempty"`
	ErrorData           *Schema          `json:"errorData,omitempty"`
}

type Schema struct {
	BaseElement
	Kind string

	Base          *BaseSchema
	Reference     *ReferenceSchema
	Array         *ArraySchema
	Or            *OrSchema
	And           *AndSchema
	Map           *MapSchema
	StringLiteral *StringLiteralSchema
	Literal       *LiteralSchema
	Tuple         *TupleSchema
}

type ArraySchema struct {
	Kind    string  `json:"kind"`
	Element *Schema `json:"element"`
}

type OrSchema struct {
	Kind  string    `json:"kind"`
	Items []*Schema `json:"items"`
}

type AndSchema struct {
	Kind  string    `json:"kind"`
	Items []*Schema `json:"items"`
}

type ReferenceSchema struct {
	Kind string `json:"kind"`
	Name string `json:"name"`

	Found *AnyRef
}

type BaseSchema struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type MapSchema struct {
	Kind  string  `json:"kind"`
	Key   *Schema `json:"key"`
	Value *Schema `json:"value"`
}

type StringLiteralSchema struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type LiteralSchema struct {
	Kind string `json:"kind"`
	// not really sure what this is, the two implementations are an empty array
	// of properties.
	Value interface{} `json:"value"`
}

type TupleSchema struct {
	Kind  string    `json:"kind"`
	Items []*Schema `json:"items"`
}

func (s *Schema) UnmarshalJSON(b []byte) error {
	explore := struct {
		Kind string `json:"kind"`
	}{}
	if err := json.Unmarshal(b, &explore); err != nil {
		return err
	}
	s.Kind = explore.Kind
	var elem interface{}
	switch s.Kind {
	case "array":
		s.Array = &ArraySchema{}
		elem = s.Array
	case "or":
		s.Or = &OrSchema{}
		elem = s.Or
	case "and":
		s.And = &AndSchema{}
		elem = s.And
	case "reference":
		s.Reference = &ReferenceSchema{}
		elem = s.Reference
	case "base":
		s.Base = &BaseSchema{}
		elem = s.Base
	case "map":
		s.Map = &MapSchema{}
		elem = s.Map
	case "stringLiteral":
		s.StringLiteral = &StringLiteralSchema{}
		elem = s.StringLiteral
	case "literal":
		s.Literal = &LiteralSchema{}
		elem = s.Literal
	case "tuple":
		s.Tuple = &TupleSchema{}
		elem = s.Tuple
	default:
		return fmt.Errorf("unknown schema kind: %s", s.Kind)
	}

	if err := unmarshalStrict(b, elem); err != nil {
		return fmt.Errorf("unmarshal schema type %s: %w", s.Kind, err)
	}
	return nil
}

func unmarshalStrict(b []byte, v interface{}) error {
	dd := json.NewDecoder(bytes.NewReader(b))
	dd.DisallowUnknownFields()
	return dd.Decode(v)
}

type MessageDirection string

const (
	ClientToServer MessageDirection = "clientToServer"
	ServerToClient MessageDirection = "serverToClient"
)
