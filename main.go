package main

import (
	"fmt"
	"math/rand"
	"sync"
	"time"
)

// Channel represents the shared ACARS frequency (131.450 MHz).
// It simulates the busy/idle state of the channel.

// Device represents an aircraft or ground station trying to send data.
type Device struct {
	id      int
	channel *Channel
	wg      *sync.WaitGroup
}

// NewDevice creates a new device.
func NewDevice(id int, ch *Channel, wg *sync.WaitGroup) *Device {
	return &Device{
		id:      id,
		channel: ch,
		wg:      wg,
	}
}

// SendMessage attempts to send a message using CSMA.
func (d *Device) SendMessage(message string) {
	defer d.wg.Done()

	maxRetries := 5
	for retry := 0; retry < maxRetries; retry++ {
		// 1. Carrier Sense (载波侦听): Check if the channel is busy
		if !d.channel.IsBusy() {
			// Channel is idle, attempt to send
			d.channel.SetBusy(true) // Mark channel as busy for the duration of transmission
			fmt.Printf("Device %d: Channel clear. Sending '%s'...\n", d.id, message)

			// Simulate transmission time (e.g., for a short ACARS message)
			// ACARS is 2400 bps. A small message (e.g., 200 bits) would take 200/2400 ~ 80ms
			time.Sleep(time.Duration(rand.Intn(100)+50) * time.Millisecond) // Simulate 50-150ms transmission

			d.channel.SetBusy(false) // Release the channel
			fmt.Printf("Device %d: Message '%s' sent. Channel released.\n", d.id, message)
			return // Message sent successfully
		}

		// 2. Channel is busy, perform random back-off (随机回退)
		backoffTime := time.Duration(rand.Intn(500)+100) * time.Millisecond // Random back-off between 100-600ms
		fmt.Printf("Device %d: Channel busy. Retrying in %v (retry %d/%d)...\n", d.id, backoffTime, retry+1, maxRetries)
		time.Sleep(backoffTime)
	}
	fmt.Printf("Device %d: Failed to send message '%s' after %d retries.\n", d.id, message, maxRetries)
}

func main() {
	fmt.Println("--- Simulating ACARS CSMA Channel ---")
	fmt.Println("Shared frequency: 131.450 MHz (abstracted)")

	channel := &Channel{isBusy: false}
	var wg sync.WaitGroup
	rand.Seed(time.Now().UnixNano()) // Initialize random seed

	// Create multiple devices (aircraft/ground stations)
	numDevices := 5
	messages := []string{
		"Flight A: Out Time",
		"Flight B: Engine Status",
		"Flight C: Fuel Report",
		"Flight D: ATIS Request",
		"Flight E: Position Report",
	}

	for i := 0; i < numDevices; i++ {
		wg.Add(1)
		device := NewDevice(i+1, channel, &wg)
		go device.SendMessage(messages[i])
	}

	// Wait for all devices to finish sending their messages
	wg.Wait()
	fmt.Println("--- Simulation finished ---")
}
