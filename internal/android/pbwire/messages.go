package pbwire

// Go shapes and codecs for every message in proto/flowbaton_android.proto, in
// the proto's own declaration order. Marshal writes fields in ascending
// field-number order and omits proto3 zero values; Unmarshal skips unknown
// fields and refuses malformed bytes. Decoded byte fields are copied, never
// subslices of the input frame, because the transport will reuse its buffer.

type DeviceInfoRequest struct{}

func (DeviceInfoRequest) Marshal() []byte { return nil }

func (*DeviceInfoRequest) Unmarshal(data []byte) error { return discardAll(data) }

type DeviceInfo struct {
	WidthPixels  uint32
	HeightPixels uint32
}

func (m DeviceInfo) Marshal() []byte {
	var b []byte
	b = appendUint32(b, 1, m.WidthPixels)
	return appendUint32(b, 2, m.HeightPixels)
}

func (m *DeviceInfo) Unmarshal(data []byte) error {
	*m = DeviceInfo{}
	for len(data) > 0 {
		field, wire, n, err := consumeTag(data)
		if err != nil {
			return err
		}
		data = data[n:]
		switch field {
		case 1:
			v, n, err := consumeVarintField(data, wire, field)
			if err != nil {
				return err
			}
			m.WidthPixels = uint32(v)
			data = data[n:]
		case 2:
			v, n, err := consumeVarintField(data, wire, field)
			if err != nil {
				return err
			}
			m.HeightPixels = uint32(v)
			data = data[n:]
		default:
			n, err := skipField(data, wire)
			if err != nil {
				return err
			}
			data = data[n:]
		}
	}
	return nil
}

type ViewHierarchyRequest struct {
	ExcludeKeyboardElements bool
}

func (m ViewHierarchyRequest) Marshal() []byte {
	return appendBool(nil, 1, m.ExcludeKeyboardElements)
}

func (m *ViewHierarchyRequest) Unmarshal(data []byte) error {
	*m = ViewHierarchyRequest{}
	for len(data) > 0 {
		field, wire, n, err := consumeTag(data)
		if err != nil {
			return err
		}
		data = data[n:]
		switch field {
		case 1:
			v, n, err := consumeVarintField(data, wire, field)
			if err != nil {
				return err
			}
			m.ExcludeKeyboardElements = v != 0
			data = data[n:]
		default:
			n, err := skipField(data, wire)
			if err != nil {
				return err
			}
			data = data[n:]
		}
	}
	return nil
}

type ViewHierarchyResponse struct {
	Hierarchy string
}

func (m ViewHierarchyResponse) Marshal() []byte {
	return appendString(nil, 1, m.Hierarchy)
}

func (m *ViewHierarchyResponse) Unmarshal(data []byte) error {
	*m = ViewHierarchyResponse{}
	for len(data) > 0 {
		field, wire, n, err := consumeTag(data)
		if err != nil {
			return err
		}
		data = data[n:]
		switch field {
		case 1:
			v, n, err := consumeBytesField(data, wire, field)
			if err != nil {
				return err
			}
			m.Hierarchy = string(v)
			data = data[n:]
		default:
			n, err := skipField(data, wire)
			if err != nil {
				return err
			}
			data = data[n:]
		}
	}
	return nil
}

type ScreenshotRequest struct{}

func (ScreenshotRequest) Marshal() []byte { return nil }

func (*ScreenshotRequest) Unmarshal(data []byte) error { return discardAll(data) }

type ScreenshotResponse struct {
	Bytes []byte
}

func (m ScreenshotResponse) Marshal() []byte {
	return appendBytes(nil, 1, m.Bytes)
}

func (m *ScreenshotResponse) Unmarshal(data []byte) error {
	*m = ScreenshotResponse{}
	for len(data) > 0 {
		field, wire, n, err := consumeTag(data)
		if err != nil {
			return err
		}
		data = data[n:]
		switch field {
		case 1:
			v, n, err := consumeBytesField(data, wire, field)
			if err != nil {
				return err
			}
			m.Bytes = append([]byte(nil), v...)
			data = data[n:]
		default:
			n, err := skipField(data, wire)
			if err != nil {
				return err
			}
			data = data[n:]
		}
	}
	return nil
}

type TapRequest struct {
	X uint32
	Y uint32
}

func (m TapRequest) Marshal() []byte {
	var b []byte
	b = appendUint32(b, 1, m.X)
	return appendUint32(b, 2, m.Y)
}

func (m *TapRequest) Unmarshal(data []byte) error {
	*m = TapRequest{}
	for len(data) > 0 {
		field, wire, n, err := consumeTag(data)
		if err != nil {
			return err
		}
		data = data[n:]
		switch field {
		case 1:
			v, n, err := consumeVarintField(data, wire, field)
			if err != nil {
				return err
			}
			m.X = uint32(v)
			data = data[n:]
		case 2:
			v, n, err := consumeVarintField(data, wire, field)
			if err != nil {
				return err
			}
			m.Y = uint32(v)
			data = data[n:]
		default:
			n, err := skipField(data, wire)
			if err != nil {
				return err
			}
			data = data[n:]
		}
	}
	return nil
}

type TapResponse struct{}

func (TapResponse) Marshal() []byte { return nil }

func (*TapResponse) Unmarshal(data []byte) error { return discardAll(data) }

type InputTextRequest struct {
	Text string
}

func (m InputTextRequest) Marshal() []byte {
	return appendString(nil, 1, m.Text)
}

func (m *InputTextRequest) Unmarshal(data []byte) error {
	*m = InputTextRequest{}
	for len(data) > 0 {
		field, wire, n, err := consumeTag(data)
		if err != nil {
			return err
		}
		data = data[n:]
		switch field {
		case 1:
			v, n, err := consumeBytesField(data, wire, field)
			if err != nil {
				return err
			}
			m.Text = string(v)
			data = data[n:]
		default:
			n, err := skipField(data, wire)
			if err != nil {
				return err
			}
			data = data[n:]
		}
	}
	return nil
}

type InputTextResponse struct{}

func (InputTextResponse) Marshal() []byte { return nil }

func (*InputTextResponse) Unmarshal(data []byte) error { return discardAll(data) }

type EraseAllTextRequest struct {
	CharactersToErase uint32
}

func (m EraseAllTextRequest) Marshal() []byte {
	return appendUint32(nil, 1, m.CharactersToErase)
}

func (m *EraseAllTextRequest) Unmarshal(data []byte) error {
	*m = EraseAllTextRequest{}
	for len(data) > 0 {
		field, wire, n, err := consumeTag(data)
		if err != nil {
			return err
		}
		data = data[n:]
		switch field {
		case 1:
			v, n, err := consumeVarintField(data, wire, field)
			if err != nil {
				return err
			}
			m.CharactersToErase = uint32(v)
			data = data[n:]
		default:
			n, err := skipField(data, wire)
			if err != nil {
				return err
			}
			data = data[n:]
		}
	}
	return nil
}

type EraseAllTextResponse struct{}

func (EraseAllTextResponse) Marshal() []byte { return nil }

func (*EraseAllTextResponse) Unmarshal(data []byte) error { return discardAll(data) }

type SetLocationRequest struct {
	Latitude  float64
	Longitude float64
}

func (m SetLocationRequest) Marshal() []byte {
	var b []byte
	b = appendDouble(b, 1, m.Latitude)
	return appendDouble(b, 2, m.Longitude)
}

func (m *SetLocationRequest) Unmarshal(data []byte) error {
	*m = SetLocationRequest{}
	for len(data) > 0 {
		field, wire, n, err := consumeTag(data)
		if err != nil {
			return err
		}
		data = data[n:]
		switch field {
		case 1:
			v, n, err := consumeDoubleField(data, wire, field)
			if err != nil {
				return err
			}
			m.Latitude = v
			data = data[n:]
		case 2:
			v, n, err := consumeDoubleField(data, wire, field)
			if err != nil {
				return err
			}
			m.Longitude = v
			data = data[n:]
		default:
			n, err := skipField(data, wire)
			if err != nil {
				return err
			}
			data = data[n:]
		}
	}
	return nil
}

type SetLocationResponse struct{}

func (SetLocationResponse) Marshal() []byte { return nil }

func (*SetLocationResponse) Unmarshal(data []byte) error { return discardAll(data) }

type CheckWindowUpdatingRequest struct {
	AppID string
}

func (m CheckWindowUpdatingRequest) Marshal() []byte {
	return appendString(nil, 1, m.AppID)
}

func (m *CheckWindowUpdatingRequest) Unmarshal(data []byte) error {
	*m = CheckWindowUpdatingRequest{}
	for len(data) > 0 {
		field, wire, n, err := consumeTag(data)
		if err != nil {
			return err
		}
		data = data[n:]
		switch field {
		case 1:
			v, n, err := consumeBytesField(data, wire, field)
			if err != nil {
				return err
			}
			m.AppID = string(v)
			data = data[n:]
		default:
			n, err := skipField(data, wire)
			if err != nil {
				return err
			}
			data = data[n:]
		}
	}
	return nil
}

type CheckWindowUpdatingResponse struct {
	IsWindowUpdating bool
}

func (m CheckWindowUpdatingResponse) Marshal() []byte {
	return appendBool(nil, 1, m.IsWindowUpdating)
}

func (m *CheckWindowUpdatingResponse) Unmarshal(data []byte) error {
	*m = CheckWindowUpdatingResponse{}
	for len(data) > 0 {
		field, wire, n, err := consumeTag(data)
		if err != nil {
			return err
		}
		data = data[n:]
		switch field {
		case 1:
			v, n, err := consumeVarintField(data, wire, field)
			if err != nil {
				return err
			}
			// Any non-zero varint is true; protobuf does not promise a 1.
			m.IsWindowUpdating = v != 0
			data = data[n:]
		default:
			n, err := skipField(data, wire)
			if err != nil {
				return err
			}
			data = data[n:]
		}
	}
	return nil
}

type LaunchAppRequest struct {
	PackageName string
	Arguments   []ArgumentValue
}

func (m LaunchAppRequest) Marshal() []byte {
	b := appendString(nil, 1, m.PackageName)
	// Repeated messages emit one length-delimited field per element, even for
	// an all-zero element: it still occupies a position in the list.
	for _, argument := range m.Arguments {
		b = appendMessage(b, 2, argument.Marshal())
	}
	return b
}

func (m *LaunchAppRequest) Unmarshal(data []byte) error {
	*m = LaunchAppRequest{}
	for len(data) > 0 {
		field, wire, n, err := consumeTag(data)
		if err != nil {
			return err
		}
		data = data[n:]
		switch field {
		case 1:
			v, n, err := consumeBytesField(data, wire, field)
			if err != nil {
				return err
			}
			m.PackageName = string(v)
			data = data[n:]
		case 2:
			v, n, err := consumeBytesField(data, wire, field)
			if err != nil {
				return err
			}
			var argument ArgumentValue
			if err := argument.Unmarshal(v); err != nil {
				return err
			}
			m.Arguments = append(m.Arguments, argument)
			data = data[n:]
		default:
			n, err := skipField(data, wire)
			if err != nil {
				return err
			}
			data = data[n:]
		}
	}
	return nil
}

type LaunchAppResponse struct{}

func (LaunchAppResponse) Marshal() []byte { return nil }

func (*LaunchAppResponse) Unmarshal(data []byte) error { return discardAll(data) }

type ArgumentValue struct {
	Key   string
	Value string
	Type  string
}

func (m ArgumentValue) Marshal() []byte {
	var b []byte
	b = appendString(b, 1, m.Key)
	b = appendString(b, 2, m.Value)
	return appendString(b, 3, m.Type)
}

func (m *ArgumentValue) Unmarshal(data []byte) error {
	*m = ArgumentValue{}
	for len(data) > 0 {
		field, wire, n, err := consumeTag(data)
		if err != nil {
			return err
		}
		data = data[n:]
		switch field {
		case 1, 2, 3:
			v, n, err := consumeBytesField(data, wire, field)
			if err != nil {
				return err
			}
			switch field {
			case 1:
				m.Key = string(v)
			case 2:
				m.Value = string(v)
			case 3:
				m.Type = string(v)
			}
			data = data[n:]
		default:
			n, err := skipField(data, wire)
			if err != nil {
				return err
			}
			data = data[n:]
		}
	}
	return nil
}

type AddMediaRequest struct {
	Payload   *Payload
	MediaName string
	MediaExt  string
}

func (m AddMediaRequest) Marshal() []byte {
	var b []byte
	if m.Payload != nil {
		b = appendMessage(b, 1, m.Payload.Marshal())
	}
	b = appendString(b, 2, m.MediaName)
	return appendString(b, 3, m.MediaExt)
}

func (m *AddMediaRequest) Unmarshal(data []byte) error {
	*m = AddMediaRequest{}
	for len(data) > 0 {
		field, wire, n, err := consumeTag(data)
		if err != nil {
			return err
		}
		data = data[n:]
		switch field {
		case 1:
			v, n, err := consumeBytesField(data, wire, field)
			if err != nil {
				return err
			}
			payload := new(Payload)
			if err := payload.Unmarshal(v); err != nil {
				return err
			}
			m.Payload = payload
			data = data[n:]
		case 2:
			v, n, err := consumeBytesField(data, wire, field)
			if err != nil {
				return err
			}
			m.MediaName = string(v)
			data = data[n:]
		case 3:
			v, n, err := consumeBytesField(data, wire, field)
			if err != nil {
				return err
			}
			m.MediaExt = string(v)
			data = data[n:]
		default:
			n, err := skipField(data, wire)
			if err != nil {
				return err
			}
			data = data[n:]
		}
	}
	return nil
}

type AddMediaResponse struct{}

func (AddMediaResponse) Marshal() []byte { return nil }

func (*AddMediaResponse) Unmarshal(data []byte) error { return discardAll(data) }

type Payload struct {
	Data []byte
}

func (m Payload) Marshal() []byte {
	return appendBytes(nil, 1, m.Data)
}

func (m *Payload) Unmarshal(data []byte) error {
	*m = Payload{}
	for len(data) > 0 {
		field, wire, n, err := consumeTag(data)
		if err != nil {
			return err
		}
		data = data[n:]
		switch field {
		case 1:
			v, n, err := consumeBytesField(data, wire, field)
			if err != nil {
				return err
			}
			m.Data = append([]byte(nil), v...)
			data = data[n:]
		default:
			n, err := skipField(data, wire)
			if err != nil {
				return err
			}
			data = data[n:]
		}
	}
	return nil
}

type EmptyRequest struct{}

func (EmptyRequest) Marshal() []byte { return nil }

func (*EmptyRequest) Unmarshal(data []byte) error { return discardAll(data) }

type EmptyResponse struct{}

func (EmptyResponse) Marshal() []byte { return nil }

func (*EmptyResponse) Unmarshal(data []byte) error { return discardAll(data) }
