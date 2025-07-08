package a

import "github.com/macabot/hypp"

func main() {
	hypp.H("div", hypp.HProps{
		"class": "foo",                             // ok
		"style": map[string]string{"color": "red"}, // ok
		"id":    "my-id",                           // ok
		"onclick": func(s any, p any) any {
			return s
		}, // ok
	})

	hypp.H("div", hypp.HProps{
		"class": struct{}{}, // want `invalid type for HProps key 'class': struct{}`
	})

	hypp.H("div", hypp.HProps{
		"style": []string{"foo"}, // want `invalid type for HProps key 'style': \[\]string`
	})

	hypp.H("div", hypp.HProps{
		"other": struct{}{}, // want `invalid type for HProps key 'other': struct{}`
	})
}
