package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"

	//"io"
	"net/http"
	"os"

	"github.com/gocarina/gocsv"
	"github.com/yanmengfei/coord"
)

type requestMsg struct {
	Latitude  float32 `json:"latitude"`
	Longitude float32 `json:"longitude"`
	GeoHash   string  `json:"geoHash"`
	IsCache   bool    `json:"isCache"`
	Address   string  `json:"address"`
}

type csvMsg struct {
	Date string  `csv:"时间点"`
	Vin  string  `csv:"车架号"`
	Cnum string  `csv:"车牌号"`
	Lng  float64 `csv:"经度"`
	Lat  float64 `csv:"纬度"`
	//Speed   int     `csv:"速度"`
	Address string `csv:"地址"`
}

type csvMsg2 struct {
	Date string  `csv:"date"`
	Vin  string  `csv:"vin"`
	Cnum string  `csv:"cnum"`
	Lng  float64 `csv:"lng"`
	Lat  float64 `csv:"lat"`
	//Speed   int     `csv:"speed"`
	Address string `csv:"address"`
}

type locationList struct {
	Lng     float64
	Lat     float64
	Address string
}

type locationList2 struct {
	Lng float64
	Lat float64
}

func main() {

	var filename string
	for _, v := range os.Args[1:] {
		//如果不为空,直接对原值转换
		if v != "" {
			filename = v
			fmt.Println(v)
			//input.Scan()
		} else {
			fmt.Println("1")

		}
	}

	if filename != "" {
		//WGS-84坐标系，GPS设备获取的经纬度坐标,地球坐标系，国际通用坐标系
		//GCJ-02坐标系，火星坐标系，WGS84坐标系加密后的坐标系；Google国内地图、高德、QQ地图等使用
		// 读取SCV文件
		records, err := readSCV(filename)
		if err != nil {
			fmt.Println("Error reading SCV file:", err)
			return
		}

		// err, file := createSCV(filename + ".tianditu")
		// if err != nil {
		// 	fmt.Println("create csv err:", err)
		// 	return
		// }
		file, err := os.OpenFile(filename, os.O_RDWR, os.ModePerm)
		if err != nil {
			fmt.Println("open csv err:", err)
			return
		}
		defer file.Close()
		w := csv.NewWriter(file)
		//var clients []*csvMsg2
		w.Write([]string{"时间点", "车牌号", "经度", "纬度", "速度", "地址"})
		// for _, record := range records {
		// 	err, address := getlocation(fmt.Sprintf("%f,%f", record.Lat, record.Lng))
		// 	if err == nil {
		// 		w.Write([]string{record.Date, record.Cnum, fmt.Sprintf("%f", record.Lng), fmt.Sprintf("%f", record.Lat), fmt.Sprintf("%d", record.Speed), address})
		// 		w.Flush()
		// 	} else {
		// 		panic(err)
		// 	}
		// }
		le := len(records)
		//var location []csvMsg2
		fmt.Println("总条数", le)
		for i, record := range records {
			if record.Address == "" {
				if err, loc := getlocation2(record); err == nil {
					fmt.Printf("%d %+v\n", i, loc) //, fmt.Sprintf("%d", loc.Speed)
					w.Write([]string{loc.Date, loc.Vin, loc.Cnum, fmt.Sprintf("%f", loc.Lng), fmt.Sprintf("%f", loc.Lat), loc.Address})
					w.Flush()
				}
			} else { //, fmt.Sprintf("%d", record.Speed)
				w.Write([]string{record.Date, record.Vin, record.Cnum, fmt.Sprintf("%f", record.Lng), fmt.Sprintf("%f", record.Lat), record.Address})
				w.Flush()
			}
		}
		// for i := 1; i <= le; i++ {
		// 	location = append(location, csvMsg2{
		// 		Date:    records[i-1].Date,
		// 		Cnum:    records[i-1].Cnum,
		// 		Lat:     records[i-1].Lat,
		// 		Lng:     records[i-1].Lng,
		// 		Speed:   records[i-1].Speed,
		// 		Address: "",
		// 	})

		// 	j := i % 20
		// 	if j == 0 {
		// 		//fmt.Printf("%+v\n", location[19:])
		// 		if err, adds := getlocations(location); err == nil {
		// 			//fmt.Printf("%+v\n", adds)
		// 			for _, ad := range adds {
		// 				fmt.Printf("%d %+v\n", i, ad)
		// 				w.Write([]string{ad.Date, ad.Cnum, fmt.Sprintf("%.6f", ad.Lng), fmt.Sprintf("%.6f", ad.Lat), fmt.Sprintf("%d", ad.Speed), ad.Address})
		// 			}
		// 			w.Flush()

		// 		} else {
		// 			fmt.Println("请求location接口错误,Err:", err)
		// 		}
		// 		location = nil

		// 	}

		// }

		// if err, adds := getlocations(location); err == nil {
		// 	//fmt.Printf("%+v\n", adds)
		// 	for _, ad := range adds {
		// 		fmt.Printf("%+v\n", ad)
		// 		w.Write([]string{ad.Date, ad.Cnum, fmt.Sprintf("%.6f", ad.Lng), fmt.Sprintf("%.6f", ad.Lat), fmt.Sprintf("%d", ad.Speed), ad.Address})
		// 	}
		// 	w.Flush()
		// 	location = nil
		// } else {
		// 	fmt.Println("请求location接口错误,Err:%s", err)
		// }

	} else {
		fmt.Println("Please input filename")
	}

}

const requestUrl string = "https://geo.util.linketech.cn/api/tianditu/reverse"

func getlocation(location string) (errs error, address string) {
	//var location = fmt.Sprintf("%f,%f", 23.5371, 113.583381)
	var lon, lat, _ = coord.LocationToFloat64Coord(location)
	//fmt.Println("WGS84:", lon, lat)
	//lon, lat = coord.WGS84toGCJ02(lon, lat)

	//fmt.Println("GCJ02:", lon, lat)

	request, err := http.NewRequest("GET", requestUrl, nil)
	if err != nil {
		fmt.Println("Error:", err)
		errs = err
		panic(err)
	}

	params := request.URL.Query()
	params.Add("coordtype", "wgs84")
	params.Add("location", fmt.Sprintf("%.6f,%.6f", lon, lat))
	params.Add("getcache", "true")
	request.URL.RawQuery = params.Encode()

	client := &http.Client{}

	response, err := client.Do(request)
	if err != nil {
		fmt.Println("Error:", err)
		errs = err
		return
		//panic(err)
	}
	defer response.Body.Close()

	var retInfo []*requestMsg
	if response.StatusCode == 200 {
		err = json.NewDecoder(response.Body).Decode(&retInfo)
		if err != nil {
			//fmt.Println("error:", string(response.Body))
			fmt.Println("Error decoding JSON:", err)
			errs = err
			address = ""
			return
		}

		if retInfo[0].Address != "" {
			//fmt.Println(location, ",", retInfo[0].Address)
			address = retInfo[0].Address
		}
	}

	return err, address
}

func getlocation2(location csvMsg) (error, csvMsg) {
	//var location = fmt.Sprintf("%f,%f", 23.5371, 113.583381)
	var lon, lat, _ = coord.LocationToFloat64Coord(fmt.Sprintf("%.6f,%.6f", location.Lat, location.Lng))
	fmt.Println("WGS84:", lon, lat)
	//lon, lat = coord.WGS84toGCJ02(lon, lat)

	//fmt.Println("GCJ02:", lon, lat)

	request, err := http.NewRequest("GET", requestUrl, nil)
	if err != nil {
		fmt.Println("Error:", err)

		//panic(err)
		return err, location
	}

	params := request.URL.Query()
	params.Add("coordtype", "wgs84")
	params.Add("location", fmt.Sprintf("%.6f,%.6f", lon, lat))
	params.Add("getcache", "false")
	request.URL.RawQuery = params.Encode()

	client := &http.Client{}

	response, err := client.Do(request)
	if err != nil {
		fmt.Println("Error:", err)

		return err, location
		//panic(err)
	}
	defer response.Body.Close()

	var retInfo []*requestMsg
	if response.StatusCode == 200 {
		err = json.NewDecoder(response.Body).Decode(&retInfo)
		if err != nil {
			fmt.Println("Error decoding JSON:", err)
			return err, location
		}

		if retInfo[0].Address != "" {
			//fmt.Println(location, ",", retInfo[0].Address)
			location.Address = retInfo[0].Address
		}
	}

	return nil, location
}

func getlocations(location []csvMsg2) (errs error, address []csvMsg2) {

	var getlocation []locationList2
	for _, loc := range location {
		var lon, lat, _ = coord.LocationToFloat64Coord(fmt.Sprintf("%.6f,%.6f", loc.Lat, loc.Lng))
		//fmt.Println("WGS84:", lon, lat)
		getlocation = append(getlocation, locationList2{
			Lat: lat,
			Lng: lon,
		})
	}

	request, err := http.NewRequest("GET", requestUrl, nil)
	if err != nil {
		fmt.Println("Error:", err)
		errs = err
		panic(err)
	}

	params := request.URL.Query()
	params.Add("coordtype", "wgs84")
	for _, loc1 := range getlocation {
		params.Add("location", fmt.Sprintf("%.6f,%.6f", loc1.Lng, loc1.Lat))
	}
	params.Add("getcache", "true")
	request.URL.RawQuery = params.Encode()

	client := &http.Client{}

	response, err := client.Do(request)
	if err != nil {
		fmt.Println("Error:", err)
		errs = err
		return errs, location
		//panic(err)
	}
	defer response.Body.Close()

	var retInfo []*requestMsg
	if response.StatusCode == 200 {
		err = json.NewDecoder(response.Body).Decode(&retInfo)
		if err != nil {
			//fmt.Println("error:", string(response.Body))
			fmt.Println("Error decoding JSON:", err)
			errs = err
			return errs, location
		}

		if retInfo != nil {
			for i, retadd := range retInfo {
				//fmt.Println(location, ",", retadd.Address)
				location[i].Address = retadd.Address
			}
		}
	}

	return err, location
}

func createSCV(filename string) (error, *os.File) {
	file, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE, os.ModePerm)
	if err != nil {
		return err, nil
	}
	defer file.Close()

	return err, file
}

func writeSCV(file *os.File, records []*csvMsg2) error {
	// file, err := os.Create(filename)
	// if err != nil {
	// 	return err
	// }
	// defer file.Close()

	// writer := csv.NewWriter(file)
	// defer writer.Flush()

	// for _, record := range records {
	// 	err := writer.Write(record)
	// 	if err != nil {
	// 		return err
	// 	}
	// }

	//clients := []*csvMsg{}
	//for _
	// err := gocsv.MarshalFile(records, file)
	// if err != nil {
	// 	return err
	// }

	if err := gocsv.MarshalFile(&records, file); err != nil {
		fmt.Println("写入文件失败:", err)
		return err
	}

	return nil
}

func readSCV(filename string) ([]csvMsg, error) {
	clients := []csvMsg{}
	//file, err := os.Open(filename)
	file, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE, os.ModePerm)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	if err := gocsv.UnmarshalFile(file, &clients); err != nil {
		return clients, err
	}

	// for _,client := range clients{

	// }
	// reader := csv.NewReader(file)

	// records, err := reader.ReadAll()
	// if err != nil {
	// 	if err == io.EOF {
	// 		return records, nil
	// 	}
	// 	return nil, err
	// }

	//return nil, nil
	return clients, nil
}
