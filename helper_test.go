package main

import (
	"testing"
)

func TestSelectContentType(t *testing.T) {
	for _, test := range []struct {
		Name             string
		Filename         string
		ExpectedType     string
		ExpectDifference bool
	}{
		{
			Name:         "text file",
			Filename:     "test.txt",
			ExpectedType: "text/plain; charset=utf-8",
		},
		{
			Name:             "file with no extension",
			Filename:         "test",
			ExpectedType:     "text/txt",
			ExpectDifference: true,
		},
	} {
		t.Run(test.Name, func(t *testing.T) {
			result := selectContentType(test.Filename)
			if result != test.ExpectedType && !test.ExpectDifference {
				t.Errorf("%+q is expected but %+q is resulting\n", test.ExpectedType, result)
			}
		})
	}
}

func Test_onlyAllowedCharacters(t *testing.T) {
	for _, test := range []struct {
		Name     string
		Filename string
		Expected string
	}{
		{
			Name:     "allowed characters",
			Filename: "test.txt",
			Expected: "test.txt",
		},
		{
			Name:     "not allowed characters",
			Filename: "test (1).txt",
			Expected: "test1.txt",
		},
		{
			Name:     "not allowed characters with spaces",
			Filename: "test foo bar.txt",
			Expected: "testfoobar.txt",
		},
		{
			Name:     "capital letters",
			Filename: "Test.txt",
			Expected: "Test.txt",
		},
	} {
		t.Run(test.Name, func(t *testing.T) {
			result := onlyAllowedCharacters(test.Filename)
			if result != test.Expected {
				t.Errorf("%+q is expected but %+q is resulting\n", test.Expected, result)
			}
		})
	}
}
