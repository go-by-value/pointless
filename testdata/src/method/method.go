package method

type Counter struct {
	N int
}

func (c *Counter) Inc() { // want Inc:"recv=direct"
	c.N++
}

func (c *Counter) Get() int { // want `Get can use a value receiver: Counter is 8 bytes and Get does not write to the receiver`
	return c.N
}

func (c *Counter) Self() *Counter { // want Self:"recv=escape"
	return c
}

func (c *Counter) Double() int { // want `Double can use a value receiver`
	return c.Get() * 2
}
