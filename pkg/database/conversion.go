package database

type realtimeConversion struct {
	replace   bool
	converter func(float64, string) (float64, bool)
	reading   float64
	unit      string
	reply     chan realtimeConversionResult
}

type realtimeConversionResult struct {
	doseRate float64
	valid    bool
}

// This immutable contact is initialized before callers start. Only its process
// owns the converter and its captures; readers exchange independent scalar data.
var realtimeConversions = startRealtimeConversions()

func startRealtimeConversions() chan<- realtimeConversion {
	requests := make(chan realtimeConversion)
	go func() {
		var converter func(float64, string) (float64, bool)
		for request := range requests {
			result := realtimeConversionResult{}
			if request.replace {
				converter = request.converter
			} else if converter != nil {
				result.doseRate, result.valid = converter(request.reading, request.unit)
			}
			request.reply <- result
		}
	}()
	return requests
}

// SetRealtimeConverter transfers the callback and its captures to the conversion
// process. The caller must relinquish mutable captures until a replacement has
// acknowledged completion. The callback must not reenter this process.
func SetRealtimeConverter(converter func(float64, string) (float64, bool)) {
	reply := make(chan realtimeConversionResult)
	realtimeConversions <- realtimeConversion{replace: true, converter: converter, reply: reply}
	<-reply
}

func convertRealtimeReading(reading float64, unit string) (float64, bool) {
	reply := make(chan realtimeConversionResult)
	realtimeConversions <- realtimeConversion{reading: reading, unit: unit, reply: reply}
	result := <-reply
	return result.doseRate, result.valid
}
