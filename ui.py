#!/usr/bin/python3
from flask import Flask, render_template, jsonify, request
import json
from multiprocessing import Process
import os

app = Flask(__name__)

fifo_path = '/usr/Brutus/test_pipe'


def send_to_fifo(device, device_name, param, value):
    message = f"{device}/{device_name}/{param} {value}"
    print(f"Sending message: {message}")
    
    if not os.path.exists(fifo_path):
        os.mkfifo(fifo_path)
    
    with open(fifo_path, 'w') as fifo:
        fifo.write(message + "\n")
        print(f"Message sent through FIFO: {message}")


def handle_data():
    print("Process started. Waiting for messages from FIFO...")
    
    if not os.path.exists(fifo_path):
        os.mkfifo(fifo_path)

    with open(fifo_path, 'r') as fifo:
        while True:
            message = fifo.readline().strip()
            
            if message:
                print(f"Received message: {message}")

def start_process():
    p = Process(target=handle_data)
    p.start()
    return p

def load_data():
    with open('/usr/Brutus/conf.json', 'r') as file:
        config = json.load(file)
    return config

@app.route('/')
def index():
    config = load_data()
    return render_template('devices.html', config=config)

@app.route('/get_data')
def get_data():
    data = load_data()
    return jsonify(data)

@app.route('/update_data/<device>/<device_name>/<param>', methods=['POST'])
def update_data(device, device_name, param):
    data = request.get_json()
    new_value = data['value']

    with open("/usr/Brutus/ui_logs.log", "a") as file:
        file.write(f"{device}/{device_name}/{param} {new_value}\n")

    send_to_fifo(device, device_name, param, new_value)

    return jsonify({"status": "success", "device": device, "device_name": device_name, "param": param, "new_value": new_value})

# if __name__ == '__main__':
process = start_process()
app.run(host='0.0.0.0', port=4321, debug=False)
process.terminate()