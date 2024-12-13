package files

import (
	"gopkg.in/yaml.v3"
	"os"
)

//type Config struct {
//	Options []map[string]map[string][]Option `yaml:",inline"`
//}
//
//type Option struct {
//	Name        string
//	Explanation string
//}

//type Select struct {
//	Selection []map[string]string `yaml:"select"` // Using a slice of maps to maintain the order
//}

func LoadConfig(filename string, structure interface{}) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, structure)
}
