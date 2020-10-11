package service

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
)

func TestDeviceExtraction(t *testing.T) {

	device, err := getDeviceBySerialID("S35ENX0J663758")
	t.Log(err)
	t.Logf("device %+v", device)

}

func TestResourceQuantity(t *testing.T) {
	b := 102400
	t.Logf("quantity of %v bytest is ", b)
	quantity := resource.NewScaledQuantity(int64(b), 0)

	t.Logf("quantity of %v bytest is ", quantity.IsZero())
	t.Logf("quantity of %v bytest is ", quantity)
}
