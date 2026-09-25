// The properties a console device's constructor and accessors read, which
// are all of one that travels to a Runner.
package console

import "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"

func init() {
	record.RegisterDispatchProperties("console_device",
		propSerialDevice, propSerialBaud, propSerialDataBits, propSerialParity, propSerialStopBits,
		propRawHost, propRawPort,
		propRFC2217Host, propRFC2217Port, propRFC2217Baud, propRFC2217DataBits, propRFC2217Parity, propRFC2217StopBits,
		propTelnetHost, propTelnetPort)
}
