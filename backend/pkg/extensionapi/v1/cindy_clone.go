package extensionv1

func CloneCindyImageRequestControls(in *CindyImageRequestControls) *CindyImageRequestControls {
	if in == nil {
		return nil
	}
	out := *in
	out.Sizes = append([]string(nil), in.Sizes...)
	out.Qualities = append([]string(nil), in.Qualities...)
	return &out
}
