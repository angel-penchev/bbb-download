package main

import (
	"bufio"
	"encoding/xml"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

type Slides struct {
	XMLName xml.Name `xml:"svg"`
	Slides  []Slide  `xml:"image"`
}

type Slide struct {
	XMLName xml.Name `xml:"image"`
	In      float64  `xml:"in,attr"`
	Out     float64  `xml:"out,attr"`
	Href    string   `xml:"href,attr"`
}

func main() {
	var webcamsFile = "webcams.webm"
	var slidesFile = "slides.mp4"

	fmt.Println("BigBlueButton` video creator/downloader")
	fmt.Print("Enter url of conference/lecture: ")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	presentationUrl := scanner.Text()

	parsedUrl, err := url.Parse(presentationUrl)
	if err != nil {
		panic(err)
	}

	urlSegments := strings.Split(parsedUrl.Path, "/")
	presentationId := urlSegments[len(urlSegments)-1]
	basePresentationUrl := parsedUrl.Scheme + "://" + parsedUrl.Host + "/presentation/" + presentationId
	shapesUrl := basePresentationUrl + "/shapes.svg"
	webcamsUrl := basePresentationUrl + "/video/"
	metadataUrl := basePresentationUrl + "/metadata.xml"

	// Reading duration of recording and meeting name from metadata.xml
	metadata := GetRequest(metadataUrl)

	duration := GetPropertyFromMetadata(metadata, "duration")
	meetingName := GetPropertyFromMetadata(metadata, "meetingName")
	fmt.Println("[Name: ", meetingName, " Duration: ", duration, "]")

	// Creating a temporary folder
	fmt.Println("Creating directory: ", presentationId, "...")
	if _, err := os.Stat(presentationId); os.IsNotExist(err) {
		os.Mkdir(presentationId, 0700) // create temporary dir
	}

	// Download webcams
	fmt.Print("Downloading webcams...", "\r")
	GetRequestWithSave(webcamsUrl+"/"+webcamsFile, presentationId+"/"+webcamsFile)
	fmt.Println(webcamsFile, " file is downloaded", "\r")

	// Verifying webcams download
	fi, err := os.Stat(presentationId + "/" + webcamsFile)
	if err != nil {
		panic(err)
	}

	fileSize := fi.Size()
	// returned 404 error text
	if fileSize < 1000 {
		// webcams.webm file is so small that real webcams file must be in mp4 format
		webcamsFile = "webcams.mp4"
		fmt.Print("Downloading webcams...", "\r")
		GetRequestWithSave(webcamsUrl+"/"+webcamsFile, presentationId+"/"+webcamsFile)
		fmt.Println(webcamsFile, "file is downloaded", "\r")
	}

	// Parse slides for in= out= href= from /shapes.svg
	var slides Slides
	xml.Unmarshal(GetRequest(shapesUrl), &slides)

	// Find and print slide timings, image Urls
	durations := make(map[int]float64)
	vidnames := make(map[int]string)
	imgnames := make(map[int]string)
	inValue, outValue, videoLength, truncated := 0.0, 0.0, 0.0, 0.0
	amountSlides := len(slides.Slides) // number of slides

	fmt.Println("Downloading slides...")
	// download all off the slides loop
	for currentSlide := 0; currentSlide < amountSlides; currentSlide++ {
		inValue = slides.Slides[currentSlide].In
		outValue = slides.Slides[currentSlide].Out
		truncated = (outValue*10 - inValue*10) / 10
		durations[currentSlide] = truncated
		imgnames[currentSlide] = "s" + strconv.Itoa(currentSlide+1) + ".png"
		vidnames[currentSlide] = "v" + strconv.Itoa(currentSlide+1) + ".mp4"

		fmt.Print("Downloading: ", imgnames[amountSlides], "\r")
		imgUrl := basePresentationUrl + "/" + slides.Slides[currentSlide].Href
		GetRequestWithSave(imgUrl, presentationId+"/"+imgnames[currentSlide])
	}

	// Correct duration of last slide
	outValue, _ = strconv.ParseFloat(duration, 64)
	outValue = outValue / 1000
	videoLength = math.Round(outValue*100) / 100
	truncated = (videoLength*10 - inValue*10) / 10
	durations[amountSlides-1] = math.Round(truncated*100) / 100
	fmt.Println("Duration of last slide according to meta.xml =", durations[amountSlides-1])

	// Create mp4 files from png files
	fmt.Println("Creating videos from slide pictures, duration is given as seconds")
	for currentSlide := 0; currentSlide <= amountSlides; currentSlide++ {
		fmt.Print(imgnames[currentSlide], " ", vidnames[currentSlide], " ", durations[currentSlide], "\r") // print to same line just like a counter
		cmd := exec.Command("ffmpeg", "-loop", "1", "-r", "5", "-f", "image2",
			"-i", presentationId+"/"+imgnames[currentSlide],
			"-c:v", "libx264", "-r", "24", "-t", fmt.Sprint(durations[currentSlide]), "-pix_fmt", "yuv420p",
			"-vf", "scale='if(gt(a,1024/768),1024,-2)':'if(gt(a,1024/768),-2,768)',pad=1024:768:(ow-iw)/2:(oh-ih)/2:color=white", // as close as 800x600
			presentationId+"/"+vidnames[currentSlide])
		cmd.Run()
	}

	if amountSlides == 1 {
		// None of the videos are merged -> just use the first slide video file
		slidesFile = "v1.mp4"
	} else {
		// If there are more than one video file -> merge them
		// Create video_list.txt file to concat with ffmpeg
		f, err := os.Create("video_list.txt")
		if err != nil {
			fmt.Println(err)
			return
		}

		for currentSlide := 1; currentSlide <= amountSlides; currentSlide++ {
			_, err := f.WriteString("file " + presentationId + "/" + vidnames[currentSlide] + "\n")
			if err != nil {
				fmt.Println(err)
				f.Close()
				return
			}
		}

		err = f.Close()
		if err != nil {
			fmt.Println(err)
			return
		}

		// Concat slide videos to create one piece of video file: slides.mp4
		fmt.Println("Merging slide videos to create: slides.mp4")
		cmd := exec.Command("ffmpeg", "-f", "concat", "-safe", "0", "-i", "video_list.txt",
			"-c", "copy", presentationId+"/"+slidesFile)
		cmd.Run()
		fmt.Println("slide videos merged")
	}

	// Convert webcams video file to webcamsRight.mp4
	fmt.Println("Converting ", webcamsFile, " to  webcamsRight.mp4")
	cmd := exec.Command("ffmpeg", "-i", presentationId+"/"+webcamsFile,
		"-q:a", "0", "-q:v", "0",
		"-vf", "scale=512:-2,pad=height=768:color=white",
		presentationId+"/"+"webcamsRight.mp4")
	cmd.Run()

	// Merging slides and webcams
	fmt.Println("Merging slides and webcams side by side...")
	cmd = exec.Command("ffmpeg", "-i", presentationId+"/"+slidesFile,
		"-i", presentationId+"/"+"webcamsRight.mp4",
		"-filter_complex", "[0:v][1:v]hstack=inputs=2[v]",
		"-t", fmt.Sprint(videoLength),
		"-map", "[v]", "-map", "1:a", meetingName+".mp4")
	cmd.Run()

	// Final clean-up
	fmt.Println("Name of the final video is: ", meetingName)
	// Deleting temporary dir
	err = os.RemoveAll(presentationId + "/")
	if err != nil {
		log.Fatal(err)
	}

	// Deleting video list file
	err = os.Remove("video_list.txt")
	if err != nil {
		log.Fatal(err)
	}
}

// GetRequest will request data from an url.
func GetRequest(url string) []byte {
	// Request the data
	res, err := http.Get(url)
	if err != nil {
		log.Fatal(err)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			log.Fatal(err)
		}
	}(res.Body)

	// Parse response body
	out, err := ioutil.ReadAll(res.Body)
	if err != nil {
		log.Fatal(err)
	}

	return out
}

// GetRequestWithSave will download a file from an url to local storage.
func GetRequestWithSave(url string, filepath string) {
	// Request the data
	res, err := http.Get(url)
	if err != nil {
		log.Fatal(err)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			log.Fatal(err)
		}
	}(res.Body)

	// Create the file on local storage
	out, err := os.Create(filepath)
	if err != nil {
		log.Fatal(err)
	}
	defer func(out *os.File) {
		err := out.Close()
		if err != nil {
			log.Fatal(err)
		}
	}(out)

	// Write the request body to file
	_, err = io.Copy(out, res.Body)
	if err != nil {
		log.Fatal(err)
	}
}

func GetPropertyFromMetadata(metadata []byte, property string) string {
	timeString := strings.SplitAfter(string(metadata), "<"+property+">")
	duration := strings.Split(timeString[1], "</"+property+">")
	return duration[0]
}
