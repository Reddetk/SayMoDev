package in

type BlackListPort interface {
	IsBlackListed(id string) bool
}
