package protocol

import (
	"encoding/binary"
	"fmt"
	"net"
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
	SessionID string   // UUID сессии
	ClientIP4 net.IP   // IPv4 клиента (может быть nil)
	ClientIP6 net.IP   // IPv6 клиента (может быть nil)
	ServerIP4 net.IP   // IPv4 сервера (может быть nil)
	ServerIP6 net.IP   // IPv6 сервера (может быть nil)
	Subnet4   byte     // маска подсети IPv4
	Subnet6   byte     // маска подсети IPv6
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
// Поддерживает оба адреса (IPv4 и IPv6) одновременно
// Формат: SessionID_len(1) + SessionID + ClientIP4_len(1) + ClientIP4 + ClientIP6_len(1) + ClientIP6
//
//	ServerIP4_len(1) + ServerIP4 + ServerIP6_len(1) + ServerIP6 + Subnet4 + Subnet6
func CreateAuthSuccessPayload(sessionID string, clientIP4, clientIP6, serverIP4, serverIP6 []byte, subnet4, subnet6 byte) []byte {
	sessionIDBytes := []byte(sessionID)
	c4Len := byte(len(clientIP4))
	c6Len := byte(len(clientIP6))
	s4Len := byte(len(serverIP4))
	s6Len := byte(len(serverIP6))

	total := 1 + len(sessionIDBytes) + // SessionID
		1 + len(clientIP4) + // ClientIP4
		1 + len(clientIP6) + // ClientIP6
		1 + len(serverIP4) + // ServerIP4
		1 + len(serverIP6) + // ServerIP6
		2 // Subnet4 + Subnet6

	payload := make([]byte, total)
	offset := 0

	// SessionID
	payload[offset] = byte(len(sessionIDBytes))
	offset++
	copy(payload[offset:], sessionIDBytes)
	offset += len(sessionIDBytes)

	// ClientIP4
	payload[offset] = c4Len
	offset++
	copy(payload[offset:], clientIP4)
	offset += len(clientIP4)

	// ClientIP6
	payload[offset] = c6Len
	offset++
	copy(payload[offset:], clientIP6)
	offset += len(clientIP6)

	// ServerIP4
	payload[offset] = s4Len
	offset++
	copy(payload[offset:], serverIP4)
	offset += len(serverIP4)

	// ServerIP6
	payload[offset] = s6Len
	offset++
	copy(payload[offset:], serverIP6)
	offset += len(serverIP6)

	// Subnets
	payload[offset] = subnet4
	offset++
	payload[offset] = subnet6

	return payload
}

// ParseAuthSuccessPayload парсит payload AUTH_SUCCESS
// Формат: SessionID_len(1) + SessionID + ClientIP4_len(1) + ClientIP4 + ClientIP6_len(1) + ClientIP6
//
//	ServerIP4_len(1) + ServerIP4 + ServerIP6_len(1) + ServerIP6 + Subnet4 + Subnet6
func ParseAuthSuccessPayload(payload []byte) (*AuthSuccessPayload, error) {
	var result AuthSuccessPayload

	if len(payload) < 1 {
		return nil, fmt.Errorf("payload too short")
	}

	offset := 0

	// SessionID
	sessionIDLen := int(payload[offset])
	offset++
	if len(payload) < offset+sessionIDLen {
		return nil, fmt.Errorf("invalid sessionID length: need %d, have %d", offset+sessionIDLen, len(payload))
	}
	result.SessionID = string(payload[offset : offset+sessionIDLen])
	offset += sessionIDLen

	// ClientIP4
	if offset >= len(payload) {
		return nil, fmt.Errorf("unexpected end of payload (ClientIP4 len)")
	}
	c4Len := int(payload[offset])
	offset++
	if c4Len > 0 {
		if offset+c4Len > len(payload) {
			return nil, fmt.Errorf("invalid ClientIP4 length")
		}
		result.ClientIP4 = make([]byte, c4Len)
		copy(result.ClientIP4, payload[offset:offset+c4Len])
		offset += c4Len
	}

	// ClientIP6
	if offset >= len(payload) {
		return nil, fmt.Errorf("unexpected end of payload (ClientIP6 len)")
	}
	c6Len := int(payload[offset])
	offset++
	if c6Len > 0 {
		if offset+c6Len > len(payload) {
			return nil, fmt.Errorf("invalid ClientIP6 length")
		}
		result.ClientIP6 = make([]byte, c6Len)
		copy(result.ClientIP6, payload[offset:offset+c6Len])
		offset += c6Len
	}

	// ServerIP4
	if offset >= len(payload) {
		return nil, fmt.Errorf("unexpected end of payload (ServerIP4 len)")
	}
	s4Len := int(payload[offset])
	offset++
	if s4Len > 0 {
		if offset+s4Len > len(payload) {
			return nil, fmt.Errorf("invalid ServerIP4 length")
		}
		result.ServerIP4 = make([]byte, s4Len)
		copy(result.ServerIP4, payload[offset:offset+s4Len])
		offset += s4Len
	}

	// ServerIP6
	if offset >= len(payload) {
		return nil, fmt.Errorf("unexpected end of payload (ServerIP6 len)")
	}
	s6Len := int(payload[offset])
	offset++
	if s6Len > 0 {
		if offset+s6Len > len(payload) {
			return nil, fmt.Errorf("invalid ServerIP6 length")
		}
		result.ServerIP6 = make([]byte, s6Len)
		copy(result.ServerIP6, payload[offset:offset+s6Len])
		offset += s6Len
	}

	// Subnets
	if offset+1 >= len(payload) {
		return nil, fmt.Errorf("unexpected end of payload (subnets)")
	}
	result.Subnet4 = payload[offset]
	offset++
	result.Subnet6 = payload[offset]

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
