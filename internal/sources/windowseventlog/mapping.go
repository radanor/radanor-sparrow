package windowseventlog

func allowed(id uint32) bool {
	switch id {
	case 1006, 1116, 1117, 1118, 1119, 5001, 5007:
		return true
	}
	return false
}
func eventType(id uint32) string {
	switch id {
	case 1006, 1116:
		return "defender.detection"
	case 1117, 1118, 1119:
		return "defender.action"
	case 5001:
		return "defender.protection_changed"
	default:
		return "defender.configuration_changed"
	}
}
