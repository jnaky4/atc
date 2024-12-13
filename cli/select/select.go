package selection

import (
	tc "filesystem/const/terminalColors"
	"fmt"
	"github.com/eiannone/keyboard"
	"os"
)

func SelectOption(header string, options []string) (string, error) {
	if len(options) == 0 {
		return "", fmt.Errorf("no options provided")
	}

	if err := keyboard.Open(); err != nil {
		return "", err
	}
	defer keyboard.Close()

	selected := 0
	fmt.Print(tc.ClearScreen)
	print(header)
	for i, option := range options {
		if i == selected {
			fmt.Printf("%s> %s%s\n", tc.Coral, option, tc.Reset)
		} else {
			fmt.Printf("%s  %s%s\n", tc.Cyan, option, tc.Reset)
		}
	}

	for {
		_, key, err := keyboard.GetKey()
		if err != nil {
			return "", err
		}

		fmt.Print(tc.ClearScreen)
		print(header)

		switch key {
		case keyboard.KeyArrowUp:
			if selected > 0 {
				selected--
			}
			for i, option := range options {
				if i == selected {
					fmt.Printf("%s> %s%s\n", tc.Coral, option, tc.Reset)
				} else {
					fmt.Printf("%s  %s%s\n", tc.Cyan, option, tc.Reset)
				}
			}
		case keyboard.KeyArrowDown:
			if selected < len(options)-1 {
				selected++
			}
			for i, option := range options {
				if i == selected {
					fmt.Printf("%s> %s%s\n", tc.Coral, option, tc.Reset)
				} else {
					fmt.Printf("%s  %s%s\n", tc.Cyan, option, tc.Reset)
				}
			}
		case keyboard.KeyArrowLeft:
			return "", nil
		case keyboard.KeyEnter:
			return options[selected], nil
		case keyboard.KeyCtrlC:
			fmt.Println("\nTerminating...")
			keyboard.Close()
			os.Exit(0)
		default:
		}
	}
}

//func main() {
//	options := []string{"Option 1", "Option 2", "Option 3", "Exit"}
//	choice, err := SelectOption(options)
//	if err != nil {
//		fmt.Println("Error:", err)
//		return
//	}
//	fmt.Println("You selected:", choice)
//}

//func main() {
//	wDir, _ := os.Getwd()
//	var selection files.Select
//	err := files.LoadConfig(wDir+"/cli/select/config.yml", &selection)
//	if err != nil {
//		fmt.Println("Error loading config -> ", err)
//		fmt.Printf("currently in %s\n", wDir)
//		return
//	}
//
//	choice, err := SelectOption(selection)
//	if err != nil {
//		fmt.Println("Error:", err)
//		return
//	}
//	fmt.Println("You selected:", choice)
//}
