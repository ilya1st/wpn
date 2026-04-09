package protocol

import (
	"encoding/binary"
	"fmt"
)

// Типы сообщений
const (
	MessageTypeData      byte = 0x01
	MessageTypeControl   byte = 0x02
	MessageTypeKeepalive byte = 0x03
	MessageTypeFragment  byte = 0x04
)

// Control подтипы
const (
	ControlTypeAuthChallenge byte = 0x10
	ControlTypeAuthResponse  byte = 0x11
	ControlTypeAuthSuccess   byte = 0x12
	ControlTypeAuthFailure   byte = 0x13
	ControlTypeRoutesConfig  byte = 0x14
	ControlTypeRoutesUpdate  byte = 0x15
	ControlTypeStatistics    byte = 0x16
	ControlTypeError         byte = 0x17
	ControlTypeDisconnect    byte = 0x18
)

// Флаги
const (
	FlagIPv6      byte = 0x01
	FlagCompressed byte = 0x02
	FlagEncrypted  byte = 0x04
)

// Error коды
const (
	ErrorCodeProtocol     byte = 0x01
	ErrorCodeFragmentTimeout byte = 0x02
	ErrorCodeInvalidPacket  byte = 0x03
	ErrorCodeRouteConflict  byte = 0x04
	ErrorCodeInternalError  byte = 0x05
)

// Константы
const (
	HeaderSize      = 4
	MaxPayloadSize  = 65535
	FragmentHeaderSize = 12
	MaxFragmentDataSize = MaxPayloadSize - FragmentHeaderSize
)

// Message представляет сообщение протокола
type Message struct {
	Type    byte
	Flags   byte
	Payload []byte
}

// FragmentHeader заголовок фрагмента
type FragmentHeader struct {
	FragmentID  uint32
	FragmentNum uint16
	TotalFrags  uint16
}

// RouteEntry запись маршрута
type RouteEntry struct {
	Dst    string
	GW     string
	Metric byte
}

// AuthSuccessPayload данные успешной аутентификации
type AuthSuccessPayload struct {
	ClientIP []byte
	ServerIP []byte
	Subnet   byte
	IsIPv6   bool
}

// StatisticsPayload данные статистики
type StatisticsPayload struct {
	BytesSent   uint64
	BytesRecv   uint64
	PacketsSent uint32
	PacketsRecv uint32
	Fragments   uint32
	Uptime      uint32
}

// Serialize сериализует сообщение в байты
func (m *Message) Serialize() []byte {
	header := make([]byte, HeaderSize)
	header[0] = m.Type
	header[1] = m.Flags
	binary.BigEndian.PutUint16(header[2:4], uint16(len(m.Payload)))

	data := make([]byte, HeaderSize+len(m.Payload))
	copy(data[:HeaderSize], header)
	copy(data[HeaderSize:], m.Payload)

	return data
}

// DeserializeMessage десериализует сообщение из байт
func DeserializeMessage(data []byte) (*Message, error) {
	if len(data) < HeaderSize {
		return nil, fmt.Errorf("data too short: %d bytes, need at least %d", len(data), HeaderSize)
	}

	msg := &Message{
		Type:  data[0],
		Flags: data[1],
	}

	payloadLen := binary.BigEndian.Uint16(data[2:4])
	if len(data) < HeaderSize+int(payloadLen) {
		return nil, fmt.Errorf("payload length mismatch: expected %d, available %d", payloadLen, len(data)-HeaderSize)
	}

	if payloadLen > 0 {
		msg.Payload = make([]byte, payloadLen)
		copy(msg.Payload, data[HeaderSize:HeaderSize+payloadLen])
	}

	return msg, nil
}

// CreateDataMessage создаёт DATA сообщение
func CreateDataMessage(packet []byte, isIPv6 bool) *Message {
	flags := byte(0)
	if isIPv6 {
		flags |= FlagIPv6
	}

	return &Message{
		Type:    MessageTypeData,
		Flags:   flags,
		Payload: packet,
	}
}

// CreateKeepaliveMessage создаёт KEEPALIVE сообщение
func CreateKeepaliveMessage() *Message {
	return &Message{
		Type:    MessageTypeKeepalive,
		Flags:   0,
		Payload: []byte{},
	}
}

// CreateControlMessage создаёт CONTROL сообщение
func CreateControlMessage(controlType byte, payload []byte) *Message {
	data := make([]byte, 1+len(payload))
	data[0] = controlType
	copy(data[1:], payload)

	return &Message{
		Type:    MessageTypeControl,
		Flags:   0,
		Payload: data,
	}
}

// CreateFragmentMessage создаёт FRAGMENT сообщение
func CreateFragmentMessage(fragmentID uint32, fragmentNum uint16, totalFrags uint16, data []byte) *Message {
	header := make([]byte, FragmentHeaderSize)
	binary.BigEndian.PutUint32(header[0:4], fragmentID)
	binary.BigEndian.PutUint16(header[4:6], fragmentNum)
	binary.BigEndian.PutUint16(header[6:8], totalFrags)
	// Reserved bytes (8-11) остаются 0

	payload := make([]byte, FragmentHeaderSize+len(data))
	copy(payload[:FragmentHeaderSize], header)
	copy(payload[FragmentHeaderSize:], data)

	return &Message{
		Type:    MessageTypeFragment,
		Flags:   0,
		Payload: payload,
	}
}

// ParseFragmentHeader парсит заголовок фрагмента
func ParseFragmentHeader(payload []byte) (*FragmentHeader, []byte, error) {
	if len(payload) < FragmentHeaderSize {
		return nil, nil, fmt.Errorf("fragment payload too short: %d bytes", len(payload))
	}

	header := &FragmentHeader{
		FragmentID:  binary.BigEndian.Uint32(payload[0:4]),
		FragmentNum: binary.BigEndian.Uint16(payload[4:6]),
		TotalFrags:  binary.BigEndian.Uint16(payload[6:8]),
	}

	data := payload[FragmentHeaderSize:]
	return header, data, nil
}

// CreateAuthResponsePayload создаёт payload для AUTH_RESPONSE
func CreateAuthResponsePayload(username, password string) []byte {
	usernameBytes := []byte(username)
	passwordBytes := []byte(password)

	payload := make([]byte, 2+len(usernameBytes)+len(passwordBytes))
	payload[0] = byte(len(usernameBytes))
	copy(payload[1:1+len(usernameBytes)], usernameBytes)
	payload[1+len(usernameBytes)] = byte(len(passwordBytes))
	copy(payload[2+len(usernameBytes):], passwordBytes)

	return payload
}

// ParseAuthResponsePayload парсит payload AUTH_RESPONSE
func ParseAuthResponsePayload(payload []byte) (username, password string, err error) {
	if len(payload) < 2 {
		return "", "", fmt.Errorf("payload too short")
	}

	usernameLen := int(payload[0])
	if len(payload) < 1+usernameLen+1 {
		return "", "", fmt.Errorf("invalid username length")
	}

	username = string(payload[1 : 1+usernameLen])
	passwordLen := int(payload[1+usernameLen])

	if len(payload) < 1+usernameLen+1+passwordLen {
		return "", "", fmt.Errorf("invalid password length: need %d, have %d", 1+usernameLen+1+passwordLen, len(payload))
	}

	password = string(payload[2+usernameLen : 2+usernameLen+passwordLen])
	return username, password, nil
}

// CreateAuthSuccessPayload создаёт payload для AUTH_SUCCESS
func CreateAuthSuccessPayload(clientIP, serverIP []byte, subnet byte) []byte {
	payload := make([]byte, len(clientIP)+len(serverIP)+1)
	copy(payload[0:len(clientIP)], clientIP)
	copy(payload[len(clientIP):len(clientIP)+len(serverIP)], serverIP)
	payload[len(clientIP)+len(serverIP)] = subnet

	return payload
}

// ParseAuthSuccessPayload парсит payload AUTH_SUCCESS
func ParseAuthSuccessPayload(payload []byte) (*AuthSuccessPayload, error) {
	var result AuthSuccessPayload

	switch len(payload) {
	case 9: // IPv4: 4+4+1
		result.IsIPv6 = false
		result.ClientIP = make([]byte, 4)
		copy(result.ClientIP, payload[0:4])
		result.ServerIP = make([]byte, 4)
		copy(result.ServerIP, payload[4:8])
		result.Subnet = payload[8]
	case 33: // IPv6: 16+16+1
		result.IsIPv6 = true
		result.ClientIP = make([]byte, 16)
		copy(result.ClientIP, payload[0:16])
		result.ServerIP = make([]byte, 16)
		copy(result.ServerIP, payload[16:32])
		result.Subnet = payload[32]
	default:
		return nil, fmt.Errorf("invalid payload size for auth success: %d", len(payload))
	}

	return &result, nil
}

// CreateErrorMessage создаёт ERROR сообщение
func CreateErrorMessage(errorCode byte, message string) *Message {
	payload := make([]byte, 1+len(message))
	payload[0] = errorCode
	copy(payload[1:], message)

	return CreateControlMessage(ControlTypeError, payload)
}

// CreateDisconnectMessage создаёт DISCONNECT сообщение
func CreateDisconnectMessage(reason string) *Message {
	payload := make([]byte, 1+len(reason))
	payload[0] = byte(len(reason))
	copy(payload[1:], reason)

	return CreateControlMessage(ControlTypeDisconnect, payload)
}

// IsIPv6 проверяет флаг IPv6
func (m *Message) IsIPv6() bool {
	return m.Flags&FlagIPv6 != 0
}

// GetControlType возвращает тип control сообщения
func (m *Message) GetControlType() (byte, error) {
	if m.Type != MessageTypeControl {
		return 0, fmt.Errorf("not a control message: type=0x%02x", m.Type)
	}
	if len(m.Payload) == 0 {
		return 0, fmt.Errorf("empty control payload")
	}
	return m.Payload[0], nil
}

// GetControlPayload возвращает payload control сообщения (без control type)
func (m *Message) GetControlPayload() ([]byte, error) {
	if m.Type != MessageTypeControl {
		return nil, fmt.Errorf("not a control message")
	}
	if len(m.Payload) < 1 {
		return nil, fmt.Errorf("empty control payload")
	}
	return m.Payload[1:], nil
}
