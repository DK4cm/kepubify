package kepub

import (
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/net/html"

	"github.com/PuerkitoBio/goquery"
	"github.com/beevik/etree"
)

// processOPF cleans up extra calibre metadata from the content.opf file, and adds a reference to the cover image.
func processOPF(opfText *string) error {
	opf := etree.NewDocument()
	err := opf.ReadFromString(*opfText)
	if err != nil {
		return err
	}

	// Add properties="cover-image" to cover file item entry to enable the kobo
	// to find the cover image.
	for _, meta := range opf.FindElements("//meta[@name='cover']") {
		coverID := meta.SelectAttrValue("content", "")
		if coverID == "" {
			coverID = "cover"
		}
		for _, item := range opf.FindElements("//[@id='" + coverID + "']") {
			item.CreateAttr("properties", "cover-image")
		}
	}

	// Remove calibre:timestamp
	for _, meta := range opf.FindElements("//meta[@name='calibre:timestamp']") {
		meta.Parent().RemoveChild(meta)
	}

	// Remove calibre contributor tag
	for _, contributor := range opf.FindElements("//dc:contributor[@role='bkp']") {
		contributor.Parent().RemoveChild(contributor)
	}

	// Pretty print OPF
	opf.Indent(4)

	// Write OPF
	*opfText, err = opf.WriteToString()
	if err != nil {
		return err
	}

	return nil
}

// addDivs adds kobo divs.
func addDivs(doc *goquery.Document) error {
	// If there are more divs than ps, divs are probably being used as paragraphs, and adding the kobo divs will most likely break the book.
	if len(doc.Find("div").Nodes) > len(doc.Find("p").Nodes) {
		return nil
	}

	// Add the kobo divs
	doc.Find("body>*").WrapAllHtml(`<div class="book-inner"></div>`)
	doc.Find("body>*").WrapAllHtml(`<div class="book-columns"></div>`)

	return nil
}

// createSpan creates a Kobo span
func createSpan(paragraph, segment int, text string) *html.Node {
	// Create the span
	span := &html.Node{
		Type: html.ElementNode,
		Data: "span",
		Attr: []html.Attribute{
			html.Attribute{
				Key: "class",
				Val: "koboSpan",
			},
			html.Attribute{
				Key: "id",
				Val: fmt.Sprintf("kobo.%v.%v", paragraph, segment),
			},
		},
	}

	// Add the text
	span.AppendChild(&html.Node{
		Type: html.TextNode,
		Data: text,
	})

	return span
}

// addSpansToNode is a recursive helper function for addSpans.
func addSpansToNode(node *html.Node, paragraph *int, segment *int) {
	sentencere := regexp.MustCompile(`((?ms).*?[\.\!\?\:]['"”’“…]?\s*)`)

	nextNodes := []*html.Node{}
	for c := node.FirstChild; c != nil; c = c.NextSibling {
		nextNodes = append(nextNodes, c)
	}

	if node.Type == html.TextNode {
		if node.Parent.Data == "pre" {
			// Do not add spans to pre elements
			return
		}
		*segment++

		sentencesindexes := sentencere.FindAllStringIndex(node.Data, -1)
		sentences := []string{}
		lasti := []int{0, 0}
		for _, i := range sentencesindexes {
			if lasti[1] != i[0] {
				// If gap in regex matches, add the gap to the sentence list to avoid losing text
				sentences = append(sentences, node.Data[lasti[1]:i[0]])
			}
			sentences = append(sentences, node.Data[i[0]:i[1]])
			lasti = i
		}
		if lasti[1] != len(node.Data) {
			// If gap in regex matches, add the gap to the sentence list to avoid losing text
			sentences = append(sentences, node.Data[lasti[1]:len(node.Data)])
		}

		for _, sentence := range sentences {
			if strings.TrimSpace(sentence) != "" {
				node.Parent.InsertBefore(createSpan(*paragraph, *segment, sentence), node)
				*segment++
			}
		}

		node.Parent.RemoveChild(node)

		return
	}

	if node.Type != html.ElementNode {
		return
	}
	if node.Data == "img" {
		return
	}
	if node.Data == "p" || node.Data == "ol" || node.Data == "ul" {
		*segment = 0
		*paragraph++
	}

	for _, c := range nextNodes {
		addSpansToNode(c, paragraph, segment)
	}
}

// addSpans adds kobo spans.
func addSpans(doc *goquery.Document) error {
	alreadyHasSpans := false
	doc.Find("span").Each(func(i int, s *goquery.Selection) {
		if val, _ := s.Attr("class"); strings.Contains(val, "koboSpan") {
			alreadyHasSpans = true
		}
	})
	if alreadyHasSpans {
		return nil
	}

	paragraph := 0
	segment := 0

	for _, n := range doc.Find("body").Nodes {
		addSpansToNode(n, &paragraph, &segment)
	}

	return nil
}

// addKoboStyles adds kobo styles.
func addKoboStyles(doc *goquery.Document) error {
	s := doc.Find("head").First().AppendHtml(`<style type="text/css">div#book-inner{margin-top: 0;margin-bottom: 0;}</style>`)
	if s.Length() != 1 {
		return fmt.Errorf("could not append kobo styles")
	}
	return nil
}

// smartenPunctuation smartens punctuation in html code. It must be run last.
func smartenPunctuation(html *string) error {
	// em and en dashes
	*html = strings.Replace(*html, "---", " &#x2013; ", -1)
	*html = strings.Replace(*html, "--", " &#x2014; ", -1)

	// TODO: smart quotes

	// Fix comments
	*html = strings.Replace(*html, "<! &#x2014; ", "<!-- ", -1)
	*html = strings.Replace(*html, " &#x2014; >", " -->", -1)
	return nil
}

// cleanHTML cleans up html for a kobo epub.
func cleanHTML(doc *goquery.Document) error {
	// Remove Adobe DRM tags
	doc.Find(`meta[name="Adept.expected.resource"]`).Remove()

	// Remove empty MS <o:p> tags
	doc.Find(`o\:p`).FilterFunction(func(_ int, s *goquery.Selection) bool {
		return strings.Trim(s.Text(), "\t \n") == ""
	}).Remove()

	// Remove empty headings
	doc.Find(`h1,h2,h3,h4,h5,h6`).FilterFunction(func(_ int, s *goquery.Selection) bool {
		return strings.Trim(s.Text(), "\t \n") == ""
	}).Remove()

	// Remove MS <st1:whatever> tags
	doc.Find(`*`).FilterFunction(func(_ int, s *goquery.Selection) bool {
		return strings.HasPrefix(goquery.NodeName(s), "st1:")
	}).Remove()

	// Open self closing p tags
	doc.Find(`p`).Each(func(_ int, s *goquery.Selection) {
		if s.Children().Length() == 0 && strings.Trim(s.Text(), "\n \t") == "" {
			s.SetHtml("")
		}
	})

	// Add type to style tags
	doc.Find(`style`).SetAttr("type", "text/css")

	return nil
}

// process processes the html of a content file in an ordinary epub and converts it into a kobo epub by adding kobo divs, kobo spans, smartening punctuation, and cleaning html.
func process(content string) (string, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(content))
	if err != nil {
		return "", err
	}

	if err := addDivs(doc); err != nil {
		return "", err
	}

	if err := addSpans(doc); err != nil {
		return "", err
	}

	if err := addKoboStyles(doc); err != nil {
		return "", err
	}

	if err := cleanHTML(doc); err != nil {
		return "", err
	}

	h, err := doc.Html()
	if err != nil {
		return "", err
	}

	if err := smartenPunctuation(&h); err != nil {
		return "", err
	}

	// Remove unicode replacement chars
	h = strings.Replace(h, "�", "", -1)

	// Fix commented xml tag
	h = strings.Replace(h, `<!-- ?xml version="1.0" encoding="utf-8"? -->`, `<?xml version="1.0" encoding="utf-8"?>`, 1)
	h = strings.Replace(h, `<!--?xml version="1.0" encoding="utf-8"?-->`, `<?xml version="1.0" encoding="utf-8"?>`, 1)

	return h, nil
}
