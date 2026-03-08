package cost

import "strings"

// monthlyPrice is the static pricing lookup table.
// Values are approximate monthly costs in USD. ±30% accuracy.
var monthlyPrice = map[string]float64{
	// RDS
	"db.t3.micro":   15,
	"db.t3.small":   30,
	"db.t3.medium":  65,
	"db.t3.large":   130,
	"db.t3.xlarge":  260,
	"db.t3.2xlarge": 520,
	"db.r6g.large":  200,
	"db.r6g.xlarge": 400,
	"db.r6g.2xlarge": 800,
	"db.r5.large":   180,
	"db.r5.xlarge":  360,
	"db.r5.2xlarge": 720,
	"db.m6g.large":  150,
	"db.m6g.xlarge": 300,

	// ElastiCache
	"cache.t3.micro":  15,
	"cache.t3.small":  30,
	"cache.t3.medium": 65,
	"cache.r6g.large": 200,
	"cache.r6g.xlarge": 400,

	// EC2
	"t3.micro":   8,
	"t3.small":   15,
	"t3.medium":  30,
	"t3.large":   60,
	"t3.xlarge":  120,
	"m6i.large":  70,
	"m6i.xlarge": 140,
	"c6i.large":  62,
	"c6i.xlarge": 124,
}

// fargatePricing constants (US regions)
const (
	fargateCPUPerHour = 0.04048  // per vCPU per hour
	fargateMemPerHour = 0.004445 // per GB per hour
	hoursPerMonth     = 730
)

// albBaseMonthlyCost is the base cost for an ALB.
const albBaseMonthlyCost = 22.0

// LookupInstancePrice returns the estimated monthly cost for an instance type.
// Returns 0 and false if the type is not in the static table.
func LookupInstancePrice(instanceType string) (float64, bool) {
	instanceType = strings.ToLower(strings.TrimSpace(instanceType))
	price, ok := monthlyPrice[instanceType]
	return price, ok
}

// EstimateFargateCost returns the monthly cost for a Fargate task.
func EstimateFargateCost(vcpus, memoryGB float64, taskCount int) float64 {
	if taskCount <= 0 {
		taskCount = 1
	}
	cpuCost := vcpus * fargateCPUPerHour * hoursPerMonth
	memCost := memoryGB * fargateMemPerHour * hoursPerMonth
	return (cpuCost + memCost) * float64(taskCount)
}

// instanceFamilyOrder defines the size ordering for suggesting cheaper alternatives.
var instanceFamilyOrder = []string{
	"micro", "small", "medium", "large", "xlarge", "2xlarge", "4xlarge", "8xlarge", "16xlarge",
}

// SuggestCheaper returns a cheaper instance type in the same family, if one exists.
func SuggestCheaper(instanceType string) (string, float64, bool) {
	instanceType = strings.ToLower(strings.TrimSpace(instanceType))
	currentPrice, ok := monthlyPrice[instanceType]
	if !ok {
		return "", 0, false
	}

	// Parse the instance family and size
	prefix, size := splitInstanceType(instanceType)
	if prefix == "" || size == "" {
		return "", 0, false
	}

	// Find the current size index
	currentIdx := -1
	for i, s := range instanceFamilyOrder {
		if s == size {
			currentIdx = i
			break
		}
	}
	if currentIdx <= 0 {
		return "", 0, false // already smallest or not found
	}

	// Try each smaller size
	for i := currentIdx - 1; i >= 0; i-- {
		candidate := prefix + instanceFamilyOrder[i]
		if price, ok := monthlyPrice[candidate]; ok {
			return candidate, currentPrice - price, true
		}
	}

	return "", 0, false
}

func splitInstanceType(instanceType string) (prefix, size string) {
	for i := len(instanceFamilyOrder) - 1; i >= 0; i-- {
		s := instanceFamilyOrder[i]
		if strings.HasSuffix(instanceType, "."+s) {
			prefix := instanceType[:len(instanceType)-len(s)]
			return prefix, s
		}
	}
	return "", ""
}
