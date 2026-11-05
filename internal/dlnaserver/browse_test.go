package dlnaserver

import (
	"encoding/xml"
	"fmt"
	"reflect"
	"testing"
)

func TestBrowsePagingPreservesCachedOrderAndExplicitSorting(t *testing.T) {
	objects := catalog{"0": {ID: "0", Container: true, Children: []string{"ten", "two", "one"}}}
	for id, name := range map[string]string{"ten": "Episode 10.mp4", "two": "Episode 2.mp4", "one": "Episode 1.mp4"} {
		objects[id] = object{ID: id, Parent: "0", Name: name, MIME: "video/mp4"}
	}
	for _, test := range []struct {
		start, count, sort string
		ids                []string
	}{
		{"0", "2", "", []string{"ten", "two"}},
		{"1", "1", "", []string{"two"}},
		{"1", "0", "", []string{"two", "one"}},
		{"99", "2", "", nil},
		{"2", "4294967295", "", []string{"one"}},
		{"1", "2", "+dc:title", []string{"two", "ten"}},
		{"1", "2", "-dc:title", []string{"two", "one"}},
	} {
		args := map[string]string{"ObjectID": "0", "BrowseFlag": "BrowseDirectChildren", "Filter": "*", "StartingIndex": test.start, "RequestedCount": test.count, "SortCriteria": test.sort}
		var prioritized []string
		body, code := browse(objects, 7, args, "http://localhost", func(items []object) {
			for _, item := range items {
				prioritized = append(prioritized, item.ID)
			}
		})
		if code != 0 {
			t.Fatal("browse failed", code)
		}
		var result soapResult
		if err := xml.Unmarshal([]byte("<response>"+body+"</response>"), &result); err != nil {
			t.Fatal(err)
		}
		var didl struct {
			Items []struct {
				ID string `xml:"id,attr"`
			} `xml:"item"`
		}
		if err := xml.Unmarshal([]byte(result.Result), &didl); err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, item := range didl.Items {
			ids = append(ids, item.ID)
		}
		if result.TotalMatches != 3 || result.NumberReturned != len(test.ids) || result.UpdateID != 7 || !reflect.DeepEqual(ids, test.ids) || !reflect.DeepEqual(prioritized, test.ids) {
			t.Fatalf("page %+v: result=%+v ids=%v priority=%v", test, result, ids, prioritized)
		}
	}
}

func BenchmarkBrowseFirstPageLargeDirectory(b *testing.B) {
	const count = 50000
	objects := make(catalog, count+1)
	root := object{ID: "0", Container: true, Children: make([]string, 0, count)}
	for i := range count {
		id := fmt.Sprint(i + 1)
		root.Children = append(root.Children, id)
		objects[id] = object{ID: id, Parent: "0", Name: "Episode " + id + ".mp4", MIME: "video/mp4", Size: 1000}
	}
	objects["0"] = root
	args := map[string]string{"ObjectID": "0", "BrowseFlag": "BrowseDirectChildren", "Filter": "*", "StartingIndex": "0", "RequestedCount": "20", "SortCriteria": ""}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, code := browse(objects, 1, args, "http://localhost", func([]object) {}); code != 0 {
			b.Fatal(code)
		}
	}
}
