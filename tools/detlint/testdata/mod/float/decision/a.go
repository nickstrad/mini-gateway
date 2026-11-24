package decision

func ShouldDrop(roll uint64, permille uint64) bool {
	ratio := float64(roll%1000) / 1000
	return ratio < float64(permille)/1000
}
